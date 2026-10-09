package iptables

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	ChainName    = "SINGBOX_MARK"
	ForwardNwIn  = "SINGBOX_NWG_IN"  // nwg0 → singtun0
	ForwardNwOut = "SINGBOX_NWG_OUT" // singtun0 → nwg0
	ForwardBrIn  = "SINGBOX_BR_IN"   // br0 → singtun0
	ForwardBrOut = "SINGBOX_BR_OUT"  // singtun0 → br0
	TableName    = "mangle"
	MarkHex      = "0x1"
	RouteTable   = "100"
	RulePref     = "1000"
	TunDev       = "singtun0"
)

const ifacesPath = "/opt/etc/sing-box/.lan_ifaces"
const routerIPPath = "/opt/etc/sing-box/.router_ip"

// DetectLANInterfaces — все интерфейсы, чей трафик перехватываем.
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

	for _, name := range []string{"br0", "br1", "br2"} {
		add(name)
	}

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

// Setup — MARK + ip rule + ip route + FORWARD + conntrack flush.
//
// vlessIPs — список IP-адресов VLESS/Trojan/Hysteria2-серверов.
// Их трафик НЕ маркируется, иначе будет петля: TUN → sing-box → TUN.
func Setup(interfaces []string, routerIP string, vlessIPs []string) error {
	if len(interfaces) == 0 {
		return fmt.Errorf("нет интерфейсов для MARK")
	}
	fmt.Printf("[iptables] MARK для %v → tun %s (искл. %s, VLESS: %v)\n",
		interfaces, TunDev, routerIP, vlessIPs)

	// === MARK ===
	run("iptables", "-t", TableName, "-N", ChainName)
	run("iptables", "-t", TableName, "-F", ChainName)

	// СНАЧАЛА исключаем VLESS-серверы — иначе петля
	for _, ip := range vlessIPs {
		if ip == "" {
			continue
		}
		run("iptables", "-t", TableName, "-A", ChainName, "-d", ip, "-j", "RETURN")
	}

	// Локальные сети — исключаем
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

	// IP роутера
	if routerIP != "" {
		run("iptables", "-t", TableName, "-A", ChainName, "-d", routerIP, "-j", "RETURN")
	}

	// Помечаем TCP + UDP
	run("iptables", "-t", TableName, "-A", ChainName,
		"-p", "tcp", "-j", "MARK", "--set-mark", MarkHex)
	run("iptables", "-t", TableName, "-A", ChainName,
		"-p", "udp", "-j", "MARK", "--set-mark", MarkHex)

	// Вставляем в PREROUTING для каждого интерфейса
	allIfaces := []string{"br0", "br1", "br2", "nwg0", "nwg1", "wg0"}
	for _, i := range allIfaces {
		run("iptables", "-t", TableName, "-D", "PREROUTING", "-i", i, "-j", ChainName)
	}
	for _, i := range interfaces {
		run("iptables", "-t", TableName, "-I", "PREROUTING", "1",
			"-i", i, "-j", ChainName)
	}

	// === FORWARD — разрешаем трафик LAN/WG → TUN и обратно ===
	setupForward()

	// === ip rule + ip route ===
	run("ip", "rule", "del", "fwmark", MarkHex, "table", RouteTable)
	run("ip", "rule", "add", "fwmark", MarkHex, "table", RouteTable, "priority", RulePref)
	run("ip", "route", "del", "default", "dev", TunDev, "table", RouteTable)
	run("ip", "route", "add", "default", "dev", TunDev, "table", RouteTable)

	// === Conntrack flush — чтобы старые сессии не «залипали» ===
	flushConntrack()

	// Сохраняем для Cleanup / NDMS hook
	_ = os.WriteFile(ifacesPath, []byte(strings.Join(interfaces, " ")), 0644)
	_ = os.WriteFile(routerIPPath, []byte(routerIP), 0644)

	fmt.Println("[iptables] правила MARK + FORWARD установлены")
	return nil
}

// setupForward — цепочки FORWARD для трафика через TUN.
func setupForward() {
	// br0 ↔ singtun0
	run("iptables", "-N", ForwardBrIn)
	run("iptables", "-N", ForwardBrOut)
	run("iptables", "-F", ForwardBrIn)
	run("iptables", "-F", ForwardBrOut)
	run("iptables", "-A", ForwardBrIn, "-j", "ACCEPT")
	run("iptables", "-A", ForwardBrOut, "-j", "ACCEPT")

	// Убираем старые правила из FORWARD
	for _, i := range []string{"br0", "nwg0"} {
		run("iptables", "-D", "FORWARD", "-i", i, "-o", TunDev, "-j", "ACCEPT")
		run("iptables", "-D", "FORWARD", "-i", TunDev, "-o", i, "-j", "ACCEPT")
	}

	// Вставляем новые в начало FORWARD
	run("iptables", "-I", "FORWARD", "1", "-i", "br0", "-o", TunDev, "-j", "ACCEPT")
	run("iptables", "-I", "FORWARD", "2", "-i", TunDev, "-o", "br0", "-j", "ACCEPT")
	run("iptables", "-I", "FORWARD", "3", "-i", "nwg0", "-o", TunDev, "-j", "ACCEPT")
	run("iptables", "-I", "FORWARD", "4", "-i", TunDev, "-o", "nwg0", "-j", "ACCEPT")
}

// flushConntrack — сбрасывает таблицу conntrack (если есть утилита).
func flushConntrack() {
	if _, err := exec.LookPath("conntrack"); err != nil {
		return
	}
	run("conntrack", "-F")
}

// Cleanup — удаляет все правила.
func Cleanup() error {
	fmt.Println("[iptables] очистка MARK")

	allIfaces := []string{"br0", "br1", "br2", "nwg0", "nwg1", "wg0"}
	if data, err := os.ReadFile(ifacesPath); err == nil {
		if s := strings.TrimSpace(string(data)); s != "" {
			allIfaces = append(allIfaces, strings.Fields(s)...)
		}
	}

	// PREROUTING
	for _, i := range allIfaces {
		run("iptables", "-t", TableName, "-D", "PREROUTING", "-i", i, "-j", ChainName)
	}
	run("iptables", "-t", TableName, "-F", ChainName)
	run("iptables", "-t", TableName, "-X", ChainName)

	// FORWARD
	for _, i := range []string{"br0", "nwg0"} {
		run("iptables", "-D", "FORWARD", "-i", i, "-o", TunDev, "-j", "ACCEPT")
		run("iptables", "-D", "FORWARD", "-i", TunDev, "-o", i, "-j", "ACCEPT")
	}
	run("iptables", "-F", ForwardBrIn)
	run("iptables", "-X", ForwardBrIn)
	run("iptables", "-F", ForwardBrOut)
	run("iptables", "-X", ForwardBrOut)

	// ip rule + ip route
	run("ip", "rule", "del", "fwmark", MarkHex, "table", RouteTable)
	run("ip", "route", "del", "default", "dev", TunDev, "table", RouteTable)

	return nil
}

// WriteNDMSHook — hook для KeeneticOS.
func WriteNDMSHook(vlessIPs []string) error {
	hookPath := "/opt/etc/ndm/netfilter.d/50-singbox.sh"

	// Формируем список VLESS-IP для hook
	var vlessRules strings.Builder
	for _, ip := range vlessIPs {
		if ip == "" {
			continue
		}
		fmt.Fprintf(&vlessRules, "iptables -t mangle -A SINGBOX_MARK -d %s -j RETURN\n", ip)
	}

	script := `#!/bin/sh
# NDMS hook: восстанавливает правила Sing-box после перестройки netfilter Keenetic.
BIN=/opt/bin/singbox-manager
[ -f "$BIN" ] || exit 0
ps -w | grep -v grep | grep -q "$BIN" || exit 0

IFACES="br0 nwg0"
[ -f /opt/etc/sing-box/.lan_ifaces ] && IFACES=$(cat /opt/etc/sing-box/.lan_ifaces)
ROUTER="192.168.35.1"
[ -f /opt/etc/sing-box/.router_ip ] && ROUTER=$(cat /opt/etc/sing-box/.router_ip)

# mangle-цепочка
iptables -t mangle -N SINGBOX_MARK 2>/dev/null
iptables -t mangle -F SINGBOX_MARK 2>/dev/null

# VLESS-серверы — исключаем (заполняется при старте менеджера)
` + vlessRules.String() + `
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

# FORWARD
iptables -D FORWARD -i br0 -o singtun0 -j ACCEPT 2>/dev/null
iptables -D FORWARD -i singtun0 -o br0 -j ACCEPT 2>/dev/null
iptables -D FORWARD -i nwg0 -o singtun0 -j ACCEPT 2>/dev/null
iptables -D FORWARD -i singtun0 -o nwg0 -j ACCEPT 2>/dev/null
iptables -I FORWARD 1 -i br0 -o singtun0 -j ACCEPT
iptables -I FORWARD 2 -i singtun0 -o br0 -j ACCEPT
iptables -I FORWARD 3 -i nwg0 -o singtun0 -j ACCEPT
iptables -I FORWARD 4 -i singtun0 -o nwg0 -j ACCEPT

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
