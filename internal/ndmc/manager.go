// Package ndmc управляет прокси-подключениями Keenetic через NDMS CLI (ndmc).
//
// ВАЖНО: KeeneticOS определяет тип интерфейса по префиксу имени.
// "Proxy" + номер = тип Proxy. Произвольные имена (например, "SingBox")
// не поддерживаются — Keenetic выдаёт "unsupported interface type".
//
// Поэтому мы используем префикс "Proxy", но с смещением BaseIndex=10,
// чтобы не конфликтовать с AWG Manager (занял Proxy0..Proxy5).
package ndmc

import (
	"fmt"
	"os/exec"
	"strings"
)

// ProxyPrefix — префикс имени прокси-интерфейса в Keenetic.
// Тип интерфейса определяется как "Proxy" (первые 5 букв).
const ProxyPrefix = "Proxy"

// BaseIndex — первый индекс прокси. Начинаем с 10, чтобы не конфликтовать
// с AWG Manager (Proxy0..Proxy5).
const BaseIndex = 10

// BasePort — базовый порт первого mixed-inbound (1080, 1081, ...).
const BasePort = 1080

// ProxyConfig — один прокси для создания.
type ProxyConfig struct {
	Index int    // 0, 1, 2, ... — смещение от BaseIndex
	Name  string // отображаемое имя (cio.vitosamin.site)
	Port  int    // порт mixed-inbound (1080, 1081, ...)
}

// Run выполняет команду ndmc.
// Игнорирует ошибки "already exists" / "not found".
func Run(args ...string) (string, error) {
	fullArgs := append([]string{"-c"}, args...)
	cmd := exec.Command("ndmc", fullArgs...)
	out, err := cmd.CombinedOutput()
	msg := strings.TrimSpace(string(out))

	// Убираем ANSI escape-последовательности для чистого сообщения
	msg = stripANSI(msg)

	if err != nil {
		if strings.Contains(msg, "already exists") ||
			strings.Contains(msg, "already running") ||
			strings.Contains(msg, "not found") ||
			strings.Contains(msg, "No such") {
			return msg, nil
		}
		return msg, fmt.Errorf("ndmc %s: %w (%s)", strings.Join(args, " "), err, msg)
	}
	return msg, nil
}

// stripANSI удаляет ANSI escape-последовательности.
func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			continue
		}
		if inEsc {
			if r == 'm' || r == 'K' {
				inEsc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// proxyName возвращает имя интерфейса для индекса.
// Index=0 → "Proxy10", Index=1 → "Proxy11", ...
func proxyName(idx int) string {
	return fmt.Sprintf("%s%d", ProxyPrefix, BaseIndex+idx)
}

// EnsureProxy создаёт один прокси.
// Index=0 → Proxy10 → 127.0.0.1:1080
// Index=1 → Proxy11 → 127.0.0.1:1081
func EnsureProxy(idx int, name string, port int) error {
	pName := proxyName(idx)
	desc := fmt.Sprintf("singbox: %s", name)

	// Идемпотентно: удаляем старый (если есть).
	_ = RemoveProxy(idx)

	// Создаём интерфейс
	if _, err := Run(fmt.Sprintf("interface %s", pName)); err != nil {
		return fmt.Errorf("create %s: %w", pName, err)
	}

	// Настройки
	steps := []struct {
		cmd string
		arg string
	}{
		{"description", fmt.Sprintf("interface %s description \"%s\"", pName, desc)},
		{"security-level", fmt.Sprintf("interface %s security-level public", pName)},
		{"proxy protocol", fmt.Sprintf("interface %s proxy protocol socks5", pName)},
		{"proxy upstream", fmt.Sprintf("interface %s proxy upstream 127.0.0.1 %d", pName, port)},
		{"proxy socks5-udp", fmt.Sprintf("interface %s proxy socks5-udp", pName)},
		{"up", fmt.Sprintf("interface %s up", pName)},
	}
	for _, step := range steps {
		if _, err := Run(step.arg); err != nil {
			return fmt.Errorf("%s %s: %w", step.cmd, pName, err)
		}
	}
	return nil
}

// RemoveProxy удаляет один прокси.
func RemoveProxy(idx int) error {
	pName := proxyName(idx)
	_, _ = Run(fmt.Sprintf("interface %s down", pName))
	_, _ = Run(fmt.Sprintf("no interface %s", pName))
	return nil
}

// SetupProxies создаёт N прокси в Keenetic.
func SetupProxies(proxies []ProxyConfig) error {
	if len(proxies) == 0 {
		return fmt.Errorf("нет прокси для создания")
	}
	fmt.Printf("[ndmc] создаём %d прокси (Proxy%d..Proxy%d)...\n",
		len(proxies), BaseIndex, BaseIndex+len(proxies)-1)

	for _, p := range proxies {
		if err := EnsureProxy(p.Index, p.Name, p.Port); err != nil {
			return fmt.Errorf("прокси %d (%s): %w", p.Index, p.Name, err)
		}
		fmt.Printf("[ndmc] %s → 127.0.0.1:%d (%s)\n",
			proxyName(p.Index), p.Port, p.Name)
	}

	if _, err := Run("system configuration save"); err != nil {
		fmt.Printf("[ndmc] предупреждение: не удалось сохранить конфиг: %v\n", err)
	}
	fmt.Println("[ndmc] все прокси созданы и сохранены")
	return nil
}

// CleanupProxies удаляет N наших прокси (индексы 0..N-1).
func CleanupProxies(count int) error {
	fmt.Printf("[ndmc] удаляем %d прокси...\n", count)
	for i := 0; i < count; i++ {
		_ = RemoveProxy(i)
	}
	_, _ = Run("system configuration save")
	fmt.Println("[ndmc] прокси удалены")
	return nil
}

// CleanupAllProxies сканирует running-config и удаляет все Proxy{BaseIndex..BaseIndex+100}.
// Это безопасно для чужих прокси (AWG Manager использует Proxy0..Proxy5,
// мы — Proxy10..Proxy99).
func CleanupAllProxies() error {
	out, err := Run("show running-config")
	if err != nil {
		return fmt.Errorf("show running-config: %w", err)
	}
	// Ищем "interface ProxyN" где N >= BaseIndex
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "interface "+ProxyPrefix) {
			continue
		}
		name := strings.TrimPrefix(line, "interface ")
		// Извлекаем номер
		numStr := strings.TrimPrefix(name, ProxyPrefix)
		var num int
		if _, err := fmt.Sscanf(numStr, "%d", &num); err != nil {
			continue
		}
		// Наши — только >= BaseIndex
		if num < BaseIndex {
			continue
		}
		_, _ = Run(fmt.Sprintf("interface %s down", name))
		_, _ = Run(fmt.Sprintf("no interface %s", name))
		fmt.Printf("[ndmc] удалён %s\n", name)
	}
	_, _ = Run("system configuration save")
	return nil
}

// IsAvailable проверяет, что ndmc доступен.
func IsAvailable() bool {
	_, err := exec.LookPath("ndmc")
	return err == nil
}

// BuildConfigs строит список ProxyConfig из серверов.
// Index=0 → Proxy10 (порт 1080)
// Index=1 → Proxy11 (порт 1081)
func BuildConfigs(serverNames []string) []ProxyConfig {
	configs := make([]ProxyConfig, 0, len(serverNames))
	for i, name := range serverNames {
		configs = append(configs, ProxyConfig{
			Index: i,
			Name:  name,
			Port:  BasePort + i,
		})
	}
	return configs
}
