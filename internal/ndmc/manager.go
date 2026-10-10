// Package ndmc управляет прокси-подключениями Keenetic через NDMS CLI (ndmc).
//
// Схема:
//   Sing-box слушает N mixed-inbound на 127.0.0.1:1080..1085+N.
//   Мы создаём N прокси-интерфейсов Keenetic (SingBox0..SingBoxN),
//   каждый из которых указывает на свой порт Sing-box.
//
// KeeneticOS сам маршрутизирует трафик в эти прокси (через HR Neo или политики).
package ndmc

import (
	"fmt"
	"os/exec"
	"strings"
)

// ProxyPrefix — префикс имени прокси-интерфейса в Keenetic.
// SingBox0, SingBox1, ... Чтобы не конфликтовать с AWG Manager (Proxy0..).
const ProxyPrefix = "SingBox"

// BasePort — базовый порт первого mixed-inbound.
// Sing-box слушает 1080, 1081, 1082, ... — по одному на каждый сервер.
const BasePort = 1080

// ProxyConfig — один прокси для создания.
type ProxyConfig struct {
	Index int    // 0, 1, 2, ...
	Name  string // отображаемое имя (например, "cio.vitosamin.site")
	Port  int    // порт mixed-inbound в Sing-box (1080, 1081, ...)
}

// Run выполняет команду ndmc.
// Игнорирует ошибки, если это ожидаемые "already exists".
func Run(args ...string) (string, error) {
	fullArgs := append([]string{"-c"}, args...)
	cmd := exec.Command("ndmc", fullArgs...)
	out, err := cmd.CombinedOutput()
	msg := strings.TrimSpace(string(out))

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

// EnsureProxy создаёт или пересоздаёт один прокси.
func EnsureProxy(idx int, name string, port int) error {
	proxyName := fmt.Sprintf("%s%d", ProxyPrefix, idx)
	desc := fmt.Sprintf("singbox: %s", name)

	// Идемпотентно: удаляем старый (если есть), создаём новый.
	_ = RemoveProxy(idx)

	if _, err := Run(fmt.Sprintf("interface %s", proxyName)); err != nil {
		return fmt.Errorf("create %s: %w", proxyName, err)
	}
	if _, err := Run(fmt.Sprintf("interface %s description \"%s\"", proxyName, desc)); err != nil {
		return fmt.Errorf("description %s: %w", proxyName, err)
	}
	if _, err := Run(fmt.Sprintf("interface %s security-level public", proxyName)); err != nil {
		return fmt.Errorf("security %s: %w", proxyName, err)
	}
	if _, err := Run(fmt.Sprintf("interface %s proxy protocol socks5", proxyName)); err != nil {
		return fmt.Errorf("protocol %s: %w", proxyName, err)
	}
	if _, err := Run(fmt.Sprintf("interface %s proxy upstream 127.0.0.1 %d", proxyName, port)); err != nil {
		return fmt.Errorf("upstream %s: %w", proxyName, err)
	}
	if _, err := Run(fmt.Sprintf("interface %s proxy socks5-udp", proxyName)); err != nil {
		return fmt.Errorf("socks5-udp %s: %w", proxyName, err)
	}
	if _, err := Run(fmt.Sprintf("interface %s up", proxyName)); err != nil {
		return fmt.Errorf("up %s: %w", proxyName, err)
	}
	return nil
}

// RemoveProxy удаляет один прокси.
func RemoveProxy(idx int) error {
	proxyName := fmt.Sprintf("%s%d", ProxyPrefix, idx)
	_, _ = Run(fmt.Sprintf("interface %s down", proxyName))
	_, _ = Run(fmt.Sprintf("no interface %s", proxyName))
	return nil
}

// SetupProxies создаёт N прокси в Keenetic.
// proxies — список серверов (индекс, имя, порт).
func SetupProxies(proxies []ProxyConfig) error {
	if len(proxies) == 0 {
		return fmt.Errorf("нет прокси для создания")
	}
	fmt.Printf("[ndmc] создаём %d прокси...\n", len(proxies))

	for _, p := range proxies {
		if err := EnsureProxy(p.Index, p.Name, p.Port); err != nil {
			return fmt.Errorf("прокси %d (%s): %w", p.Index, p.Name, err)
		}
		fmt.Printf("[ndmc] %s%d → 127.0.0.1:%d (%s)\n", ProxyPrefix, p.Index, p.Port, p.Name)
	}

	// Сохраняем конфиг Keenetic
	if _, err := Run("system configuration save"); err != nil {
		fmt.Printf("[ndmc] предупреждение: не удалось сохранить конфиг: %v\n", err)
	}
	fmt.Println("[ndmc] все прокси созданы и сохранены")
	return nil
}

// CleanupProxies удаляет все наши прокси.
// Идёт по индексам 0..N и пытается удалить.
func CleanupProxies(count int) error {
	fmt.Printf("[ndmc] удаляем до %d прокси...\n", count)
	for i := 0; i < count; i++ {
		_ = RemoveProxy(i)
	}
	_, _ = Run("system configuration save")
	fmt.Println("[ndmc] прокси удалены")
	return nil
}

// CleanupAllProxies удаляет все прокси с нашим префиксом.
// Вызывается при деинсталляции. Сканирует running-config.
func CleanupAllProxies() error {
	out, err := Run("show running-config")
	if err != nil {
		return fmt.Errorf("show running-config: %w", err)
	}
	// Ищем "interface SingBoxN"
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "interface "+ProxyPrefix) {
			continue
		}
		name := strings.TrimPrefix(line, "interface ")
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
// Каждому серверу — свой индекс и порт.
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
