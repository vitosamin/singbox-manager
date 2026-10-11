package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/shapovalenko/keenetic-singbox-manager/internal/auth"
	"github.com/shapovalenko/keenetic-singbox-manager/internal/config"
	"github.com/shapovalenko/keenetic-singbox-manager/internal/failover"
	"github.com/shapovalenko/keenetic-singbox-manager/internal/iptables"
	"github.com/shapovalenko/keenetic-singbox-manager/internal/monitor"
	"github.com/shapovalenko/keenetic-singbox-manager/internal/ruleset"
	"github.com/shapovalenko/keenetic-singbox-manager/internal/singbox"
	"github.com/shapovalenko/keenetic-singbox-manager/internal/state"
	"github.com/shapovalenko/keenetic-singbox-manager/internal/subscription"
	"github.com/shapovalenko/keenetic-singbox-manager/internal/vpn"
	"github.com/shapovalenko/keenetic-singbox-manager/internal/watchdog"
)

//go:embed templates/* static/*
var content embed.FS

var (
	store     = state.NewStore()
	authMgr   *auth.Manager
	monitorMu sync.Mutex
)

// Start запускает HTTP-сервер.
func Start(addr string) error {
	if err := store.Load(); err != nil {
		fmt.Printf("[web] предупреждение: %v\n", err)
	} else {
		st := store.Snapshot()
		fmt.Printf("[web] состояние: %d прокси, %d списков, %d групп, %d custom, %d custom-srs, %d dns, %d devices\n",
			len(st.Proxies), len(st.EnabledLists), len(st.Groups), len(st.CustomRules),
			len(st.CustomSRS), len(st.DNS.Servers), len(st.Devices))
		singbox.SetLogEnabled(st.Settings.LogEnabled)
		if st.Settings.LogRingSize > 0 {
			singbox.SetLogRingSize(st.Settings.LogRingSize)
		}
	}

	var defaultPassword string
	authMgr, defaultPassword = auth.NewManager(store.PasswordHash())
	if defaultPassword != "" {
		fmt.Println("═══════════════════════════════════════════════════════════")
		fmt.Printf("  ПЕРВЫЙ ЗАПУСК: пароль по умолчанию = %q\n", defaultPassword)
		fmt.Println("  Смените его в Настройках после входа!")
		fmt.Println("═══════════════════════════════════════════════════════════")
		if err := store.SetPasswordHash(authMgr.PasswordHash()); err != nil {
			fmt.Printf("[auth] не удалось сохранить хеш пароля: %v\n", err)
		}
	}
	auth.OnPasswordHashChange = func(hash string) {
		store.SetPasswordHash(hash)
	}

	mux := http.NewServeMux()

	// --- Auth (публичные) ---
	mux.HandleFunc("/api/auth/login", authMgr.HandleLogin)

	// --- Auth (защищённые) ---
	mux.HandleFunc("/api/auth/logout", authMgr.HandleLogout)
	mux.HandleFunc("/api/auth/status", authMgr.HandleStatus)
	mux.HandleFunc("/api/auth/change-password", func(w http.ResponseWriter, r *http.Request) {
		authMgr.HandleChangePassword(w, r)
		if authMgr != nil {
			store.SetPasswordHash(authMgr.PasswordHash())
		}
	})

	// --- API ---
	mux.HandleFunc("/api/state", handleState)
	mux.HandleFunc("/api/catalog", handleCatalog)
	mux.HandleFunc("/api/subscription", handleSubscription)
	mux.HandleFunc("/api/proxies/add", handleProxyAdd)
	mux.HandleFunc("/api/proxies/remove", handleProxyRemove)
	mux.HandleFunc("/api/proxies/test", handleProxyTest)
	mux.HandleFunc("/api/lists/toggle", handleToggleList)
	mux.HandleFunc("/api/groups", handleGroups)
	mux.HandleFunc("/api/groups/add", handleGroupAdd)
	mux.HandleFunc("/api/groups/remove", handleGroupRemove)
	mux.HandleFunc("/api/custom-rules", handleCustomRules)
	mux.HandleFunc("/api/custom-srs", handleCustomSRS)
	mux.HandleFunc("/api/dns", handleDNS)
	mux.HandleFunc("/api/devices", handleDevices)
	mux.HandleFunc("/api/theme", handleTheme)
	mux.HandleFunc("/api/settings", handleSettings)
	mux.HandleFunc("/api/monitor", handleMonitor)
	mux.HandleFunc("/api/logs", handleLogs)
	mux.HandleFunc("/api/restart", handleRestart)
	mux.HandleFunc("/api/export", handleExport)
	mux.HandleFunc("/api/import", handleImport)
	mux.HandleFunc("/api/apply", handleApply)
	mux.HandleFunc("/api/rulesets/status", handleRulesetsStatus)
	mux.HandleFunc("/api/rulesets/refresh", handleRulesetsRefresh)

	// VPN Stop/Start/Status + Firewall Check
	mux.HandleFunc("/api/vpn/stop", handleVPNStop)
	mux.HandleFunc("/api/vpn/start", handleVPNStart)
	mux.HandleFunc("/api/vpn/status", handleVPNStatus)
	mux.HandleFunc("/api/firewall/check", handleFirewallCheck)

	tmplFS, _ := fs.Sub(content, "templates")
	staticFS, _ := fs.Sub(content, "static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	mux.Handle("/", http.FileServer(http.FS(tmplFS)))

	handler := auth.Middleware(authMgr, mux)

	fmt.Printf("[web] сервер запущен на http://%s\n", addr)

	// Запускаем watchdog sing-box.
	// Регистрируем callback: если watchdog уронит sing-box — обновить state.
	watchdog.SetOnFailure(func() {
		_ = store.SetVPNEnabled(false)
	})

	watchdog.Start()

	return http.ListenAndServe(addr, handler)
}

// handleState — отдаёт всё состояние.
func handleState(w http.ResponseWriter, r *http.Request) {
	st := store.Snapshot()
	writeJSON(w, map[string]interface{}{
		"proxies":      st.Proxies,
		"enabled":      st.EnabledLists,
		"subscription": st.Subscription,
		"groups":       st.Groups,
		"custom_rules": st.CustomRules,
		"custom_srs":   st.CustomSRS,
		"dns":          st.DNS,
		"devices":      st.Devices,
		"theme":        st.Theme,
		"settings":     st.Settings,
		"vpn_enabled":  st.VPNEnabled,
	})
}

// handleCatalog — каталог rule-set.
func handleCatalog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, ruleset.Catalog)
}

// handleSubscription — парсит и ДОБАВЛЯЕТ прокси из подписки.
func handleSubscription(w http.ResponseWriter, r *http.Request) {
	var body struct{ Text string `json:"text"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	text := strings.TrimSpace(body.Text)
	if text == "" {
		writeJSON(w, map[string]interface{}{"ok": true, "count": 0, "message": "пустая подписка — пропущено"})
		return
	}

	proxies, err := subscription.Parse(text)
	if err != nil {
		writeJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}

	if err := store.SetSubscription(text, proxies); err != nil {
		writeJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]interface{}{"proxies": proxies, "count": len(proxies)})
}

// handleProxyAdd — добавить один сервер.
func handleProxyAdd(w http.ResponseWriter, r *http.Request) {
	var body struct{ Raw string `json:"raw"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	added, err := store.AddProxy(body.Raw)
	if err != nil {
		writeJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true, "added": added})
}

// handleProxyRemove — удалить сервер по тегу.
func handleProxyRemove(w http.ResponseWriter, r *http.Request) {
	var body struct{ Tag string `json:"tag"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := store.RemoveProxy(body.Tag); err != nil {
		writeJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true})
}

// handleProxyTest — проверить сервер через Clash API.
func handleProxyTest(w http.ResponseWriter, r *http.Request) {
	var body struct{ Tag string `json:"tag"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	client := &http.Client{Timeout: 8 * time.Second}
	url := fmt.Sprintf("http://127.0.0.1:9090/proxies/%s/delay?timeout=5000&url=https://www.google.com/generate_204", body.Tag)
	resp, err := client.Get(url)
	if err != nil {
		writeJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	defer resp.Body.Close()
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	writeJSON(w, result)
}

// handleToggleList — включить/выключить rule-set.
func handleToggleList(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	st := store.Snapshot()
	ids := st.EnabledLists
	if body.Enabled {
		if !contains(ids, body.ID) {
			ids = append(ids, body.ID)
		}
	} else {
		ids = remove(ids, body.ID)
	}
	if err := store.SetEnabledLists(ids); err != nil {
		writeJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]interface{}{"enabled": ids})
}

// handleGroups — сохранить приоритеты.
func handleGroups(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Groups config.GroupConfig `json:"groups"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := store.SetGroups(body.Groups); err != nil {
		writeJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true})
}

// handleGroupAdd — создать ручную группу.
func handleGroupAdd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name  string `json:"name"`
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if body.Name == "" {
		writeJSON(w, map[string]interface{}{"error": "имя группы не может быть пустым"})
		return
	}
	if err := store.AddManualGroup(body.Name, body.Label); err != nil {
		writeJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true})
}

// handleGroupRemove — удалить ручную группу.
func handleGroupRemove(w http.ResponseWriter, r *http.Request) {
	var body struct{ Name string `json:"name"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := store.RemoveManualGroup(body.Name); err != nil {
		writeJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true})
}

// handleCustomRules — сохранить свои правила.
func handleCustomRules(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Rules []ruleset.CustomRule `json:"rules"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := store.SetCustomRules(body.Rules); err != nil {
		writeJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true, "rules": body.Rules})
}

// handleCustomSRS — сохранить свои .srs.
func handleCustomSRS(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Items []ruleset.CustomSRS `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := store.SetCustomSRS(body.Items); err != nil {
		writeJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true, "items": body.Items})
}

// handleDNS — GET/POST DNS-конфига.
func handleDNS(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		st := store.Snapshot()
		writeJSON(w, st.DNS)
		return
	}
	var body config.DNSConfig
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := store.SetDNS(body); err != nil {
		writeJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true, "dns": body})
}

// handleDevices — сохранить маршрутизацию устройств.
func handleDevices(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Devices []config.DeviceRule `json:"devices"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := store.SetDevices(body.Devices); err != nil {
		writeJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true, "devices": body.Devices})
}

// handleTheme — сохранить тему.
func handleTheme(w http.ResponseWriter, r *http.Request) {
	var body struct{ Theme string `json:"theme"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := store.SetTheme(body.Theme); err != nil {
		writeJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true, "theme": body.Theme})
}

// handleSettings — GET/POST настроек.
func handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		st := store.Snapshot()
		writeJSON(w, st.Settings)
		return
	}
	var body config.Settings
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := store.SetSettings(body); err != nil {
		writeJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	singbox.SetLogEnabled(body.LogEnabled)
	if body.LogRingSize > 0 {
		singbox.SetLogRingSize(body.LogRingSize)
	}
	writeJSON(w, map[string]interface{}{"ok": true, "settings": body})
}

// handleMonitor — статус всех outbound'ов.
func handleMonitor(w http.ResponseWriter, r *http.Request) {
	statuses, err := monitor.FetchAll()
	if err != nil {
		writeJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]interface{}{"proxies": statuses})
}

// handleLogs — GET/DELETE журнала Sing-box.
func handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		singbox.LogRing.Clear()
		writeJSON(w, map[string]interface{}{"ok": true})
		return
	}
	text := singbox.LogRing.Tail(500)
	if len(text) == 0 {
		if data, err := os.ReadFile("/opt/var/log/sing-box.log"); err == nil {
			s := string(data)
			if len(s) > 50000 {
				s = s[len(s)-50000:]
			}
			text = s
		}
	}
	writeJSON(w, map[string]interface{}{"text": text})
}

// handleRestart — перезапуск Sing-box без пересборки конфига.
func handleRestart(w http.ResponseWriter, r *http.Request) {
	if err := singbox.Restart(nil); err != nil {
		writeJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true})
}

// handleExport — экспорт state.json.
func handleExport(w http.ResponseWriter, r *http.Request) {
	data, err := store.RawJSON()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="singbox-state.json"`)
	w.Write(data)
}

// handleImport — импорт state.json.
func handleImport(w http.ResponseWriter, r *http.Request) {
	data, err := readAll(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := store.ReplaceFromJSON(data); err != nil {
		writeJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true})
}

// handleVPNStop — снимает iptables-правила (VPN выключается, sing-box работает).
func handleVPNStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := vpn.Stop(); err != nil {
		writeJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	_ = store.SetVPNEnabled(false)
	writeJSON(w, map[string]interface{}{"ok": true, "enabled": false})
}

// handleVPNStart — восстанавливает iptables-правила (VPN включается).
func handleVPNStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	st := store.Snapshot()
	serverHosts := make([]string, 0, len(st.Proxies))
	for _, p := range st.Proxies {
		if p.Server != "" {
			serverHosts = append(serverHosts, p.Server)
		}
	}
	vlessIPs := singbox.ResolveServerIPs(serverHosts)

	if err := vpn.Start(vlessIPs); err != nil {
		writeJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	_ = store.SetVPNEnabled(true)
	writeJSON(w, map[string]interface{}{"ok": true, "enabled": true})
}

// handleVPNStatus — возвращает текущий статус VPN.
func handleVPNStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]interface{}{
		"enabled": store.VPNEnabled(),
	})
}

// handleFirewallCheck — проверяет цепочки iptables и восстанавливает, если пропали.
func handleFirewallCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if vpn.IsDisabled() {
		writeJSON(w, map[string]interface{}{
			"ok":      true,
			"message": "VPN выключен, проверка пропущена",
			"changed": false,
		})
		return
	}

	st := store.Snapshot()
	serverHosts := make([]string, 0, len(st.Proxies))
	for _, p := range st.Proxies {
		if p.Server != "" {
			serverHosts = append(serverHosts, p.Server)
		}
	}
	vlessIPs := singbox.ResolveServerIPs(serverHosts)

	changed, err := vpn.Check(vlessIPs)
	if err != nil {
		writeJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	msg := "Правила в порядке"
	if changed {
		msg = "Правила восстановлены"
	}
	writeJSON(w, map[string]interface{}{
		"ok":      true,
		"message": msg,
		"changed": changed,
	})
}

// handleApply — ГЛАВНОЕ:
//   1. Скачивает .srs (мягко — при ошибке использует кэш)
//   2. Генерирует config.d/
//   3. Резолвит IP серверов и ставит iptables-правила (TProxy + REDIRECT)
//   4. Запускает Sing-box с -C config.d
func handleApply(w http.ResponseWriter, r *http.Request) {
	// Уведомляем watchdog, что идёт Apply (grace-period 10 сек).
	watchdog.SetApplyInProgress(true)
	defer watchdog.SetApplyInProgress(false)

	st := store.Snapshot()

	if len(st.Proxies) == 0 {
		writeJSON(w, map[string]interface{}{"error": "нет серверов — сначала добавь хотя бы один"})
		return
	}

	// 1. Скачиваем .srs (МЯГКО — ошибка не отменяет Apply).
	var srsWarnings []string
	for _, id := range st.EnabledLists {
		if _, err := ruleset.DownloadByID(id); err != nil {
			msg := fmt.Sprintf("список %s: %v", id, err)
			srsWarnings = append(srsWarnings, msg)
			fmt.Printf("[web] предупреждение: %s\n", msg)
			// НЕ отменяем Apply
		}
	}
	for _, srs := range st.CustomSRS {
		if _, err := ruleset.Download(srs.ID, srs.URL); err != nil {
			msg := fmt.Sprintf("custom .srs %s: %v", srs.Name, err)
			srsWarnings = append(srsWarnings, msg)
			fmt.Printf("[web] предупреждение: %s\n", msg)
			// НЕ отменяем Apply
		}
	}

	// 2. Генерируем config.d/
	if err := config.GenerateV3(
		st.EnabledLists, st.CustomRules, st.CustomSRS,
		st.Proxies, st.Groups, st.DNS, st.Devices,
		singbox.InstallDir(),
	); err != nil {
		writeJSON(w, map[string]interface{}{"error": fmt.Sprintf("generate: %v", err)})
		return
	}

	// 3. Резолвим IP VLESS-серверов
	serverHosts := make([]string, 0, len(st.Proxies))
	for _, p := range st.Proxies {
		if p.Server != "" {
			serverHosts = append(serverHosts, p.Server)
		}
	}
	vlessIPs := singbox.ResolveServerIPs(serverHosts)
	fmt.Printf("[web] IP VLESS-серверов: %v\n", vlessIPs)

	// 4. Ставим iptables-правила (TProxy + REDIRECT)
	if err := iptables.Setup(vlessIPs); err != nil {
		writeJSON(w, map[string]interface{}{"error": fmt.Sprintf("iptables: %v", err)})
		return
	}

	// 5. Запускаем Sing-box с -C config.d
	if st.Settings.RestartOnApply {
		if err := singbox.Restart(serverHosts); err != nil {
			writeJSON(w, map[string]interface{}{"error": fmt.Sprintf("restart: %v", err)})
			return
		}
		startMonitor(st.Groups)
	}

	resp := map[string]interface{}{
		"ok":      true,
		"lists":   len(st.EnabledLists),
		"proxies": len(st.Proxies),
	}
	if len(srsWarnings) > 0 {
		resp["warnings"] = srsWarnings
	}
	writeJSON(w, resp)
}

// startMonitor — запускает failover-монитор в фоне.
func startMonitor(groups config.GroupConfig) {
	monitorMu.Lock()
	defer monitorMu.Unlock()
	var mGroups []failover.Group
	for name, g := range groups {
		tags := g.PriorityTags()
		if len(tags) == 0 {
			continue
		}
		mGroups = append(mGroups, failover.Group{
			SelectorTag:  "proxy-" + name,
			PriorityTags: tags,
		})
	}
	if len(mGroups) == 0 {
		return
	}
	go func() {
		time.Sleep(5 * time.Second)
		m := failover.NewMonitor(mGroups)
		m.Run()
	}()
	fmt.Printf("[web] запущен монитор failover для %d групп\n", len(mGroups))
}

// writeJSON — хелпер для JSON-ответов.
func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

// contains — проверка вхождения строки в массив.
func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// remove — удаление строки из массива.
func remove(s []string, v string) []string {
	out := s[:0]
	for _, x := range s {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

// readAll — чтение тела запроса.
func readAll(r *http.Request) ([]byte, error) {
	const maxSize = 5 * 1024 * 1024
	body := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for {
		n, err := r.Body.Read(buf)
		if n > 0 {
			body = append(body, buf[:n]...)
			if len(body) > maxSize {
				return nil, fmt.Errorf("файл слишком большой")
			}
		}
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, err
		}
	}
	return body, nil
}
