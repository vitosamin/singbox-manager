// Package vpn управляет включением/выключением VPN.
//
// Логика:
//   - Stop: снимает iptables-правила (трафик идёт напрямую) + ставит маркер
//     /opt/etc/sing-box/vpn.disabled. sing-box продолжает работать.
//   - Start: восстанавливает iptables-правила + удаляет маркер.
//
// Маркер нужен, чтобы NDMS-хук и cron-скрипт НЕ восстанавливали правила,
// пока VPN выключен пользователем.
package vpn

import (
	"fmt"
	"os"

	"github.com/shapovalenko/keenetic-singbox-manager/internal/iptables"
)

const (
	// MarkerPath — файл-маркер, что VPN выключен.
	MarkerPath = "/opt/etc/sing-box/vpn.disabled"
)

// Stop — снимает iptables-правила, ставит маркер.
// sing-box продолжает работать (просто не получает трафик).
func Stop() error {
	fmt.Println("[vpn] Stop: снимаем iptables-правила")

	// 1. Ставим маркер ДО снятия правил — чтобы NDMS-хук не восстановил.
	if err := writeMarker(); err != nil {
		return fmt.Errorf("write marker: %w", err)
	}

	// 2. Снимаем iptables-правила.
	if err := iptables.Cleanup(); err != nil {
		// Не откатываем маркер — пусть будет выключено.
		return fmt.Errorf("iptables cleanup: %w", err)
	}

	fmt.Println("[vpn] VPN выключен (правила сняты, sing-box работает)")
	return nil
}

// Start — восстанавливает iptables-правила, удаляет маркер.
func Start(vlessIPs []string) error {
	fmt.Println("[vpn] Start: восстанавливаем iptables-правила")

	// 1. Удаляем маркер ДО восстановления.
	if err := removeMarker(); err != nil {
		return fmt.Errorf("remove marker: %w", err)
	}

	// 2. Восстанавливаем iptables-правила.
	if err := iptables.Setup(vlessIPs); err != nil {
		// Если не получилось — возвращаем маркер, чтобы не было «полу-включено».
		_ = writeMarker()
		return fmt.Errorf("iptables setup: %w", err)
	}

	fmt.Println("[vpn] VPN включён")
	return nil
}

// Check — проверяет, что iptables-правила на месте.
// Если нет — восстанавливает (по vlessIPs).
// Возвращает: (были ли проблемы, ошибка).
func Check(vlessIPs []string) (bool, error) {
	natOK := chainExists("SB_TCP", "nat")
	mangleOK := chainExists("SB_UDP_TRANSIT", "mangle")

	if natOK && mangleOK {
		return false, nil
	}

	fmt.Printf("[vpn] Check: цепочки повреждены (nat=%v, mangle=%v) — восстанавливаем\n", natOK, mangleOK)

	// Если маркер стоит — значит, VPN выключен, не восстанавливаем.
	if IsDisabled() {
		return true, nil
	}

	if err := iptables.Setup(vlessIPs); err != nil {
		return true, fmt.Errorf("iptables setup: %w", err)
	}
	return true, nil
}

// IsDisabled — проверяет наличие маркера.
func IsDisabled() bool {
	_, err := os.Stat(MarkerPath)
	return err == nil
}

// writeMarker — создаёт файл-маркер.
func writeMarker() error {
	_ = os.MkdirAll("/opt/etc/sing-box", 0755)
	return os.WriteFile(MarkerPath, []byte("VPN disabled by user\n"), 0644)
}

// removeMarker — удаляет файл-маркер.
func removeMarker() error {
	err := os.Remove(MarkerPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// chainExists — проверяет, существует ли iptables-цепочка.
func chainExists(chain, table string) bool {
	err := iptables.CheckChain(chain, table)
	return err == nil
}
