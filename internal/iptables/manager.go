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

const ifacesPath = "/opt/etc/sing-box/.lan_ifaces"
const routerIPPath = "/opt/etc/sing-box/.router_ip"

// DetectLANInterfaces — находит все интерфейсы, трафик которых нужно перехватывать.
// Это LAN (br0, br1, br2) + WireGuard (nwg0, nwg1, wg0, ...).
func DetectLANInterfaces() []string {
	var result []string
	seen := make(map[string]bool)

	add := func(name string) {
		if !seen[name] {
			if _, err := os.Stat("/sys/class/net/" + name); err == nil {
				result = append(result, name)
				seen[name] = true
			}
		}
	}

	// LAN
	for _, name := range []string{"br0", "br1", "br2"} {
		add(name)
	}

	// WireGuard (nwg* — native WireGuard в Keenetic; wg* — стандартный)
	entries, err := os.ReadDir("/sys/class/net/")
	if err == nil {
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, "nwg") || strings.HasPrefix(name, "wg") {
				add(name)
			}
		}
	}

	if len(result) == 0 {
		result = []string{"br0"}
	}
	return result
}

// DetectRouterIP — IP роутера в LAN.
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

// Setup — создаёт MARK-правила для всех перечисленных интерфейсов.
func Setup(interfaces []string, routerIP string) error {
	if len(interfaces) == 0 {
		return fmt.Errorf("нет интерфейсов для MARK")
	}
	fmt.Printf("[iptables] MARK для %v → tun %s (искл. %s)\n", interfaces, TunDev, routerIP)

	// 1. Создаём цепочку
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

	// 3. IP роутера
	if routerIP != "" {
		run("iptables", "-t", TableName, "-A", ChainName, "-d", routerIP, "-j", "RETURN")
	}

	// 4. Помечаем TCP+UDP
	run("iptables", "-t", TableName, "-A", ChainName,
		"-p", "tcp", "-j", "MARK", "--set-mark", MarkHex)
	run("iptables", "-t", TableName, "-A", ChainName,
		"-p", "udp", "-j", "MARK", "--set-mark", MarkHex)

	// 5. Вставляем в PREROUTING для КАЖДОГО интерфейса
	// Сначала удаляем старые (по всем возможным)
	allIfaces := []string{"br0", "br1", "br2", "nwg0", "nwg1", "wg0"}
	for _, i := range allIfaces {
		run("iptables", "-t", TableName, "-D", "PREROUTING", "-i", i, "-j", ChainName)
	}
	for _, i := range interfaces {
		run("iptables", "-t", TableName, "-I", "PREROUTING", "1",
			"-i", i, "-j", ChainName)
	}

	// 6. ip rule: fwmark 0x1 → table 100
	run("ip", "rule", "del", "fwmark", MarkHex, "table", RouteTable)
	run("ip", "rule", "add", "fwmark", MarkHex, "table", RouteTable, "priority", RulePref)

	// 7. ip route: default dev singtun0 в table 100
	run("ip", "route", "del", "default", "dev", TunDev, "table", RouteTable)
	run("ip", "route", "add", "default", "dev", TunDev, "table", RouteTable)

	// 8. Сохраняем параметры для Cleanup / NDMS hook
	_ = os.WriteFile(ifacesPath, []byte(strings.Join(interfaces, " ")), 0644)
	_ = os.WriteFile(routerIPPath, []byte(routerIP), 0644)

	fmt.Println("[iptables] правила MARK установлены")
	return nil
}

// Cleanup — удаляет все правила.
func Cleanup() error {
	fmt.Println("[iptables] очистка MARK")

	// Все возможные интерфейсы
	allIfaces := []string{"br0", "br1", "br2", "nwg0", "nwg1", "wg0"}

	// Читаем сохранённые
	if data, err := os.ReadFile(ifacesPath); err == nil {
		if s := strings.TrimSpace(string(data)); s != "" {
			allIfaces = append(allIfaces, strings.Fields(s)...)
		}
	}

	for _, i := range allIfaces {
		run("iptables", "-t", TableName, "-D", "PREROUTING", "-i", i, "-j", ChainName)
	}

	run("iptables", "-t", TableName, "-F", ChainName)
	run("iptables", "-t", TableName, "-X", ChainName)

	run("ip", "rule", "del", "fwmark", MarkHex, "table", RouteTable)
	run("ip", "route", "del", "default", "dev", TunDev, "table", RouteTable)

	return nil
}

// WriteNDMSHook — создаёт hook для KeeneticOS.
func WriteNDMSHook() error {
	hookPath := "/opt/etc/ndm/netfilter.d/50-singbox.sh"
	script := `#!/bin/sh
# NDMS hook: восстанавливает правила Sing-box после перестройки netfilter Keenetic.
BIN=/opt/bin/singbox-manager
[ -f "$BIN" ] || exit 0
ps -w | grep -v grep | grep -q "$BIN" || exit 0

# Читаем сохранённые параметры
IFACES="br0 nwg0"
[ -f /opt/etc/sing-box/.lan_ifaces ] && IFACES=$(cat /opt/etc/sing-box/.lan_ifaces)
ROUTER="192.168.35.1"
[ -f /opt/etc/sing-box/.router_ip ] && ROUTER=$(cat /opt/etc/sing-box/.router_ip)

# mangle-цепочка
iptables -t mangle -N SINGBOX_MARK 2>/dev/null
iptables -t mangle -F SINGBOX_MARK 2>/dev/null

for cidr in 0.0.0.0/8 10.0.0.0/8 127.0.0.0/8 169.254.0.0/16 172.16.0.0/12 192.168.0.0/16 224.0.0.0/4 240.0.0.0/4; do
    iptables -t mangle -A SINGBOX_MARK -d $cidr -j RETURN
done
[ -n "$ROUTER" ] && iptables -t mangle -A SINGBOX_MARK -d $ROUTER -j RETURN
iptables -t mangle -A SINGBOX_MARK -p tcp -j MARK --set-mark 0x1
iptables -t mangle -A SINGBOX_MARK -p udp -j MARK --set-mark 0x1

for i in $IFACES; do
    iptables -t mangle -D PREROUTING -i $i -j SINGBOX_MARK 2>/dev/null
    iptables -t mangle -I PREROUTING 1 -i $i -j SINGBOX_MARK 2>/dev/null
done

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
			strings.Contains(msg, "Bad rule") {
			return
		}
		fmt.Printf("[iptables] %s %v: %v (%s)\n", name, args, err, strings.TrimSpace(msg))
	}
}
