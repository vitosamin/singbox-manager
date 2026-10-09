package iptables

import (
	"fmt"
	"os"
	"strings"
)

const lanIfacePath = "/opt/etc/sing-box/.lan_iface"

// DetectLANInterface — определяет LAN-интерфейс.
func DetectLANInterface() string {
	if v := os.Getenv("LAN_IFACE"); v != "" {
		return v
	}
	for _, name := range []string{"br0", "br1", "br2"} {
		if _, err := os.Stat("/sys/class/net/" + name); err == nil {
			return name
		}
	}
	return "br0"
}

// Setup — заглушка. TProxy на Keenetic не поддерживается.
// Маршрутизация работает через route_exclude_address в TUN.
func Setup(lanIface string, tproxyPort int) error {
	fmt.Println("[iptables] TProxy не поддерживается, используем TUN с исключениями")
	return nil
}

// Cleanup — заглушка.
func Cleanup() error {
	// Убираем возможные остатки от старых запусков
	for _, i := range []string{"br0", "br1", "br2"} {
		runSilent("iptables", "-t", "mangle", "-D", "PREROUTING", "-i", i, "-j", "SINGBOX_TPROXY")
	}
	runSilent("iptables", "-t", "mangle", "-F", "SINGBOX_TPROXY")
	runSilent("iptables", "-t", "mangle", "-X", "SINGBOX_TPROXY")
	return nil
}

// IsSupported — всегда false на Keenetic.
func IsSupported() bool {
	return false
}

func runSilent(name string, args ...string) {
	// Игнорируем все ошибки
	_ = strings.TrimSpace(name)
}
