package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"sync"
	"time"

	"github.com/shapovalenko/keenetic-singbox-manager/internal/auth"
	"github.com/shapovalenko/keenetic-singbox-manager/internal/config"
	"github.com/shapovalenko/keenetic-singbox-manager/internal/failover"
	"github.com/shapovalenko/keenetic-singbox-manager/internal/monitor"
	"github.com/shapovalenko/keenetic-singbox-manager/internal/ruleset"
	"github.com/shapovalenko/keenetic-singbox-manager/internal/singbox"
	"github.com/shapovalenko/keenetic-singbox-manager/internal/state"
	"github.com/shapovalenko/keenetic-singbox-manager/internal/subscription"
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

	// Инициализируем auth
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

	// --- API (защищённые) ---
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

	tmplFS, _ := fs.Sub(content, "templates")
	staticFS, _ := fs.Sub(content, "static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	mux.Handle("/", http.FileServer(http.FS(tmplFS)))

	// Оборачиваем в auth middleware
	handler := auth.Middleware(authMgr, mux)

	fmt.Printf("[web] сервер запущен на http://%s\n", addr)
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
	})
}

// handleCatalog — каталог rule-set.
func handleCatalog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, ruleset.Catalog)
}

// handleSubscription — парсит и сохраняет подписку.
func handleSubscription(w http.ResponseWriter, r *http.Request) {
	var body struct{ Text string `json:"text"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	proxies, err := subscription.Parse(body.Text)
	if err != nil {
		writeJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}
	if err := store.SetSubscription(body.Text, proxies); err != nil {
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
	writeJSON(w, map[string]interface{}{"text": text})
}

// handleRestart — перезапуск Sing-box без пересборки конфига.
func handleRestart(w http.ResponseWriter, r *http.Request) {
	st := store.Snapshot()
	var serverHosts []string
	for _, p := range st.Proxies {
		if p.Server != "" {
			serverHosts = append(serverHosts, p.Server)
		}
	}
	if err := singbox.Restart(serverHosts); err != nil {
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

// handleApply — ГЛАВНОЕ: пересобирает конфиг и перезапускает Sing-box.
func handleApply(w http.ResponseWriter, r *http.Request) {
	st := store.Snapshot()

	for _, id := range st.EnabledLists {
		if _, err := ruleset.DownloadByID(id); err != nil {
			writeJSON(w, map[string]interface{}{"error": fmt.Sprintf("download %s: %v", id, err)})
			return
		}
	}

	for _, srs := range st.CustomSRS {
		if _, err := ruleset.Download(srs.ID, srs.URL); err != nil {
			writeJSON(w, map[string]interface{}{"error": fmt.Sprintf("download custom %s: %v", srs.Name, err)})
			return
		}
	}

	if err := config.Generate(
		st.EnabledLists, st.CustomRules, st.CustomSRS,
		st.Proxies, st.Groups, st.DNS, st.Devices, singbox.ConfigPath(),
	); err != nil {
		writeJSON(w, map[string]interface{}{"error": fmt.Sprintf("generate: %v", err)})
		return
	}

	// Собираем список серверов для исключения из MARK
	var serverHosts []string
	for _, p := range st.Proxies {
		if p.Server != "" {
			serverHosts = append(serverHosts, p.Server)
		}
	}
	fmt.Printf("[web] серверы для исключения из MARK: %v\n", serverHosts)

	if st.Settings.RestartOnApply {
		if err := singbox.Restart(serverHosts); err != nil {
			writeJSON(w, map[string]interface{}{"error": fmt.Sprintf("restart: %v", err)})
			return
		}
		startMonitor(st.Groups)
	}

	writeJSON(w, map[string]interface{}{
		"ok":      true,
		"lists":   len(st.EnabledLists),
		"proxies": len(st.Proxies),
	})
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
