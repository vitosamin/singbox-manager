package iptables

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	ChainName  = "SINGBOX_MARK"
	TableName  = "mangle"
	MarkHex    = "0x1"
	RouteTable = "100"
	RulePref   = "1000"
	TunDev     = "singtun0"
)

const lanIfacePath = "/opt/etc/sing-box/.lan_iface"
const routerIPPath = "/opt/etc/sing-box/.router_ip"

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

// DetectRouterIP — находит IP роутера в LAN.
func DetectRouterIP(lanIface string) string {
	if v := os.Getenv("ROUTER_IP"); v != "" {
		return v
	}
	out, err := exec.Command("sh", "-c",
		fmt.Sprintf("ip -4 addr show %s | grep -oE 'inet [0-9.]+' | awk '{print $2}' | head -1", lanIface)).Output()
	if err == nil {
		ip := strings.TrimSpace(string(out))
		if ip != "" {
			return ip
		}
	}
	return "192.168.35.1"
}

// Setup — настраивает MARK + ip rule + ip route.
// Схема:
//   1. mangle PREROUTING -i br0 -j SINGBOX_MARK
//   2. В SINGBOX_MARK исключаем локальные сети и IP роутера
//   3. Остальное MARK 0x1
//   4. ip rule: fwmark 0x1 lookup 100 priority 1000
//   5. ip route: default dev singtun0 table 100
// Main table НЕ ТРОГАЕМ — поэтому SSH/веб роутера работают.
func Setup(lanIface, routerIP string) error {
	fmt.Printf("[iptables] MARK: %s → tun %s (искл. %s)\n", lanIface, TunDev, routerIP)

	// 1. Создаём цепочку в mangle
	run("iptables", "-t", TableName, "-N", ChainName)
	run("iptables", "-t", TableName, "-F", ChainName)

	// 2. Исключения — локальные сети
	for _, cidr := range []string{
		"0.0.0.0/8",
		"10.0.0.0/8",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"224.0.0.0/4",
		"240.0.0.0/4",
	} {
		run("iptables", "-t", TableName, "-A", ChainName, "-d", cidr, "-j", "RETURN")
	}

	// 3. Отдельно — IP роутера
	if routerIP != "" {
		run("iptables", "-t", TableName, "-A", ChainName, "-d", routerIP, "-j", "RETURN")
	}

	// 4. Помечаем весь оставшийся трафик (TCP + UDP)
	run("iptables", "-t", TableName, "-A", ChainName,
		"-p", "tcp", "-j", "MARK", "--set-mark", MarkHex)
	run("iptables", "-t", TableName, "-A", ChainName,
		"-p", "udp", "-j", "MARK", "--set-mark", MarkHex)

	// 5. Вставляем в начало PREROUTING для LAN-интерфейса
	for _, i := range []string{"br0", "br1", "br2"} {
		run("iptables", "-t", TableName, "-D", "PREROUTING", "-i", i, "-j", ChainName)
	}
	run("iptables", "-t", TableName, "-I", "PREROUTING", "1",
		"-i", lanIface, "-j", ChainName)

	// 6. ip rule: fwmark 0x1 → table 100
	run("ip", "rule", "del", "fwmark", MarkHex, "table", RouteTable)
	run("ip", "rule", "add", "fwmark", MarkHex, "table", RouteTable, "priority", RulePref)

	// 7. ip route: default dev singtun0 в table 100
	run("ip", "route", "add", "default", "dev", TunDev, "table", RouteTable)

	// Сохраняем параметры
	_ = os.WriteFile(lanIfacePath, []byte(lanIface), 0644)
	_ = os.WriteFile(routerIPPath, []byte(routerIP), 0644)

	fmt.Println("[iptables] правила MARK установлены")
	return nil
}

// Cleanup — удаляет все правила.
func Cleanup() error {
	fmt.Println("[iptables] очистка MARK")

	lanIface := "br0"
	if data, err := os.ReadFile(lanIfacePath); err == nil {
		if s := strings.TrimSpace(string(data)); s != "" {
			lanIface = s
		}
	}

	for _, i := range []string{lanIface, "br0", "br1", "br2"} {
		run("iptables", "-t", TableName, "-D", "PREROUTING", "-i", i, "-j", ChainName)
	}

	run("iptables", "-t", TableName, "-F", ChainName)
	run("iptables", "-t", TableName, "-X", ChainName)

	run("ip", "rule", "del", "fwmark", MarkHex, "table", RouteTable)
	run("ip", "route", "del", "default", "dev", TunDev, "table", RouteTable)

	return nil
}

// WriteNDMSHook — создаёт hook для KeeneticOS.
// Keenetic периодически пересобирает netfilter и стирает наши правила.
// Этот скрипт вызывается Keenetic после перестройки и восстанавливает правила.
func WriteNDMSHook() error {
	hookPath := "/opt/etc/ndm/netfilter.d/50-singbox.sh"
	script := `#!/bin/sh
# NDMS hook для восстановления правил Sing-box Manager.
# KeeneticOS вызывает этот скрипт после перестройки netfilter.
# Не запускать, если менеджер не работает.

BIN=/opt/bin/singbox-manager
[ -f "$BIN" ] || exit 0

# Проверяем, что singbox-manager запущен
if ! ps -w | grep -v grep | grep -q "$BIN"; then
    exit 0
fi

LAN_IFACE=/opt/etc/sing-box/.lan_iface
[ -f "$LAN_IFACE" ] && LAN=$(cat "$LAN_IFACE") || LAN=br0

ROUTER_IP=/opt/etc/sing-box/.router_ip
[ -f "$ROUTER_IP" ] && ROUTER=$(cat "$ROUTER_IP") || ROUTER=192.168.35.1

# Восстанавливаем цепочку mangle
iptables -t mangle -N SINGBOX_MARK 2>/dev/null
iptables -t mangle -F SINGBOX_MARK 2>/dev/null

for cidr in 0.0.0.0/8 10.0.0.0/8 127.0.0.0/8 169.254.0.0/16 172.16.0.0/12 192.168.0.0/16 224.0.0.0/4 240.0.0.0/4; do
    iptables -t mangle -A SINGBOX_MARK -d $cidr -j RETURN
done
[ -n "$ROUTER" ] && iptables -t mangle -A SINGBOX_MARK -d $ROUTER -j RETURN
iptables -t mangle -A SINGBOX_MARK -p tcp -j MARK --set-mark 0x1
iptables -t mangle -A SINGBOX_MARK -p udp -j MARK --set-mark 0x1

iptables -t mangle -D PREROUTING -i $LAN -j SINGBOX_MARK 2>/dev/null
iptables -t mangle -I PREROUTING 1 -i $LAN -j SINGBOX_MARK 2>/dev/null

ip rule del fwmark 0x1 table 100 2>/dev/null
ip rule add fwmark 0x1 table 100 priority 1000 2>/dev/null
ip route add default dev singtun0 table 100 2>/dev/null
`
	if err := os.WriteFile(hookPath, []byte(script), 0755); err != nil {
		return fmt.Errorf("write hook: %w", err)
	}
	fmt.Println("[iptables] NDMS hook записан:", hookPath)
	return nil
}

// RemoveNDMSHook — удаляет hook.
func RemoveNDMSHook() {
	_ = os.Remove("/opt/etc/ndm/netfilter.d/50-singbox.sh")
}

func run(name string, args ...string) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := string(out)
		if strings.Contains(msg, "File exists") ||
			strings.Contains(msg, "No such file") ||
			strings.Contains(msg, "No chain") ||
			strings.Contains(msg, "does not exist") ||
			strings.Contains(msg, "Bad rule") ||
			strings.Contains(msg, "File exists") {
			return
		}
		fmt.Printf("[iptables] %s %v: %v (%s)\n", name, args, err, strings.TrimSpace(msg))
	}
}
