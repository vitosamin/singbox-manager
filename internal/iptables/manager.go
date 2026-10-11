// Package iptables управляет iptables-правилами для прозрачного проксирования
// трафика через sing-box (TProxy + REDIRECT).
//
// Схема:
//   - TCP → nat PREROUTING → REDIRECT :1082
//   - UDP → mangle PREROUTING → TPROXY :1081
//
// Правила создаются в отдельных цепочках SB_TCP и SB_UDP_TRANSIT.
// При перестройке netfilter KeeneticOS вызывает NDMS-хук, который
// перезапускает S55singbox-tproxy.sh.
package iptables

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	// Цепочки
	ChainTCP = "SB_TCP"
	ChainUDP = "SB_UDP_TRANSIT"

	// Порты Sing-box
	TCPRedirectPort = 1082 // nat REDIRECT
	UDPProxyPort    = 1081 // mangle TPROXY

	// Метка и таблица маршрутизации для TPROXY
	TProxyMark  = "1"
	TProxyTable = "100"

	// Пути
	iptablesNatPath    = "/opt/etc/sing-box/.iptables_nat.sh"
	iptablesManglePath = "/opt/etc/sing-box/.iptables_mangle.sh"
	ndmsHookPath       = "/opt/etc/ndm/netfilter.d/50-singbox.sh"
	tproxyInitPath     = "/opt/etc/init.d/S55singbox-tproxy.sh"
)

// DetectLANInterfaces — возвращает список LAN/WG-интерфейсов.
// Трафик с этих интерфейсов перехватываем.
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

	// WireGuard
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

// Setup устанавливает iptables-правила для Sing-box TProxy + REDIRECT.
// vlessIPs — список IP VLESS-серверов (их трафик НЕ перехватываем, чтобы не было петли).
func Setup(vlessIPs []string) error {
	fmt.Printf("[iptables] Setup: TProxy+REDIRECT (VLESS IPs: %v)\n", vlessIPs)

	// Загружаем модули ядра
	loadKernelModules()

	// Готовим содержимое скриптов
	natScript := buildNatScript(vlessIPs)
	mangleScript := buildMangleScript(vlessIPs)

	// Создаём каталог (если его нет)
	_ = os.MkdirAll("/opt/etc/sing-box", 0755)

	// Записываем скрипты на диск
	if err := os.WriteFile(iptablesNatPath, []byte(natScript), 0755); err != nil {
		return fmt.Errorf("write nat script: %w", err)
	}
	if err := os.WriteFile(iptablesManglePath, []byte(mangleScript), 0755); err != nil {
		return fmt.Errorf("write mangle script: %w", err)
	}

	// Применяем правила напрямую
	if err := runScript(iptablesNatPath); err != nil {
		return fmt.Errorf("apply nat: %w", err)
	}
	if err := runScript(iptablesManglePath); err != nil {
		return fmt.Errorf("apply mangle: %w", err)
	}

	// Init-скрипт S55singbox-tproxy.sh (для автозапуска и NDMS-хука)
	if err := WriteTProxyInit(); err != nil {
		fmt.Printf("[iptables] предупреждение: tproxy init: %v\n", err)
	}

	// NDMS-хук
	if err := WriteNDMSHook(); err != nil {
		fmt.Printf("[iptables] предупреждение: NDMS hook: %v\n", err)
	}

	fmt.Println("[iptables] правила TProxy+REDIRECT установлены")
	return nil
}

// Cleanup удаляет правила.
func Cleanup() error {
	fmt.Println("[iptables] Cleanup")

	interfaces := []string{"br0", "br1", "br2", "nwg0", "nwg1", "wg0"}

	// nat
	for _, i := range interfaces {
		run("iptables", "-t", "nat", "-D", "PREROUTING", "-i", i, "-p", "tcp", "-j", ChainTCP)
	}
	run("iptables", "-t", "nat", "-F", ChainTCP)
	run("iptables", "-t", "nat", "-X", ChainTCP)

	// mangle
	for _, i := range interfaces {
		run("iptables", "-t", "mangle", "-D", "PREROUTING", "-i", i, "-p", "udp", "-j", ChainUDP)
	}
	run("iptables", "-t", "mangle", "-F", ChainUDP)
	run("iptables", "-t", "mangle", "-X", ChainUDP)

	// ip rule + route
	for {
		cmd := exec.Command("ip", "rule", "del", "fwmark", TProxyMark, "table", TProxyTable)
		if err := cmd.Run(); err != nil {
			break
		}
	}
	run("ip", "route", "del", "local", "default", "dev", "lo", "table", TProxyTable)

	// NDMS hook
	_ = os.Remove(ndmsHookPath)

	return nil
}

// CheckChain — проверяет, существует ли цепочка в указанной таблице.
// Возвращает nil, если цепочка есть.
func CheckChain(chain, table string) error {
	cmd := exec.Command("iptables", "-t", table, "-L", chain, "-n")
	return cmd.Run()
}

// WriteNDMSHook — создаёт хук для KeeneticOS.
func WriteNDMSHook() error {
	script := `#!/bin/sh
# NDMS hook: восстанавливает правила Sing-box после перестройки netfilter Keenetic.
#
# KeeneticOS вызывает этот скрипт с аргументами:
#   $1 = iptables | ip6tables
#   $2 = nat | mangle | filter
#
# Мы работаем только для iptables + nat/mangle.

type="$1"
table="$2"

# Только iptables
[ "$type" != "iptables" ] && exit 0

# Только nat и mangle
[ "$table" != "nat" ] && [ "$table" != "mangle" ] && exit 0

# VPN выключен — не восстанавливаем
[ -f /opt/etc/sing-box/vpn.disabled ] && exit 0

# Sing-box должен быть запущен
ps -w | grep -v grep | grep -q "sing-box" || exit 0

# Retry: до 5 попыток, каждые 2 секунды, пока ОБЕ цепочки не будут на месте
for i in 1 2 3 4 5; do
    nat_ok=0
    mangle_ok=0
    iptables -t nat -L SB_TCP -n >/dev/null 2>&1 && nat_ok=1
    iptables -t mangle -L SB_UDP_TRANSIT -n >/dev/null 2>&1 && mangle_ok=1

    if [ "$nat_ok" = "1" ] && [ "$mangle_ok" = "1" ]; then
        logger -t singbox-ndm "netfilter rules OK (type=$type table=$table, attempt=$i)"
        exit 0
    fi

    sleep 2
    if [ -x /opt/etc/init.d/S55singbox-tproxy.sh ]; then
        /opt/etc/init.d/S55singbox-tproxy.sh start >/dev/null 2>&1
    fi
done

logger -t singbox-ndm "netfilter rules NOT restored after 5 attempts (type=$type table=$table)"
`
	if err := os.MkdirAll("/opt/etc/ndm/netfilter.d", 0755); err != nil {
		return err
	}
	if err := os.WriteFile(ndmsHookPath, []byte(script), 0755); err != nil {
		return fmt.Errorf("write ndm hook: %w", err)
	}
	fmt.Println("[iptables] NDMS hook записан:", ndmsHookPath)
	return nil
}

// WriteTProxyInit — создаёт init-скрипт S55singbox-tproxy.sh.
// Он применяет/снимает iptables-правила.
func WriteTProxyInit() error {
	script := `#!/bin/sh
# Применяет iptables-правила для Sing-box TProxy + REDIRECT.

NAT_SCRIPT="` + iptablesNatPath + `"
MANGLE_SCRIPT="` + iptablesManglePath + `"
CHAIN_TCP="` + ChainTCP + `"
CHAIN_UDP="` + ChainUDP + `"
MARK="` + TProxyMark + `"
TABLE="` + TProxyTable + `"

case "$1" in
    start)
        echo "Applying Sing-box iptables rules..."
        KERNEL=$(uname -r)
        insmod /lib/modules/$KERNEL/xt_TPROXY.ko 2>/dev/null
        insmod /lib/modules/$KERNEL/xt_socket.ko 2>/dev/null
        [ -x "$NAT_SCRIPT" ] && sh "$NAT_SCRIPT"
        [ -x "$MANGLE_SCRIPT" ] && sh "$MANGLE_SCRIPT"
        echo "OK"
        ;;
    stop)
        echo "Removing Sing-box iptables rules..."
        for i in br0 br1 br2 nwg0 nwg1 wg0; do
            iptables -t nat -D PREROUTING -i $i -p tcp -j $CHAIN_TCP 2>/dev/null
            iptables -t mangle -D PREROUTING -i $i -p udp -j $CHAIN_UDP 2>/dev/null
        done
        iptables -t nat -F $CHAIN_TCP 2>/dev/null
        iptables -t nat -X $CHAIN_TCP 2>/dev/null
        iptables -t mangle -F $CHAIN_UDP 2>/dev/null
        iptables -t mangle -X $CHAIN_UDP 2>/dev/null
        while ip rule del fwmark $MARK table $TABLE 2>/dev/null; do :; done
        ip route del local default dev lo table $TABLE 2>/dev/null
        echo "OK"
        ;;
    restart)
        $0 stop
        sleep 1
        $0 start
        ;;
    status)
        iptables -t nat -L $CHAIN_TCP -n 2>/dev/null && echo "TCP: OK" || echo "TCP: NOT SET"
        iptables -t mangle -L $CHAIN_UDP -n 2>/dev/null && echo "UDP: OK" || echo "UDP: NOT SET"
        ;;
    *)
        echo "Usage: $0 {start|stop|restart|status}"
        exit 1
        ;;
esac
exit 0
`
	if err := os.WriteFile(tproxyInitPath, []byte(script), 0755); err != nil {
		return fmt.Errorf("write tproxy init: %w", err)
	}
	fmt.Println("[iptables] init script:", tproxyInitPath)
	return nil
}

// loadKernelModules — загружает xt_TPROXY и xt_socket.
func loadKernelModules() {
	run("insmod", "/lib/modules/"+kernelRelease()+"/xt_TPROXY.ko")
	run("insmod", "/lib/modules/"+kernelRelease()+"/xt_socket.ko")
}

func kernelRelease() string {
	out, err := exec.Command("uname", "-r").Output()
	if err != nil {
		return "4.9-ndm-5"
	}
	return strings.TrimSpace(string(out))
}

// buildNatScript — скрипт для nat PREROUTING (TCP REDIRECT).
func buildNatScript(vlessIPs []string) string {
	var sb strings.Builder
	sb.WriteString("#!/bin/sh\n")
	sb.WriteString("# TCP REDIRECT → Sing-box\n\n")

	sb.WriteString("iptables -t nat -N " + ChainTCP + " 2>/dev/null\n")
	sb.WriteString("iptables -t nat -F " + ChainTCP + "\n")

	// Исключения
	for _, cidr := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "127.0.0.0/8", "169.254.0.0/16",
		"172.16.0.0/12", "192.168.0.0/16", "224.0.0.0/4", "240.0.0.0/4",
	} {
		sb.WriteString("iptables -t nat -A " + ChainTCP + " -d " + cidr + " -j RETURN\n")
	}

	// VLESS IP
	for _, ip := range vlessIPs {
		if ip == "" {
			continue
		}
		sb.WriteString("iptables -t nat -A " + ChainTCP + " -d " + ip + " -j RETURN\n")
	}

	sb.WriteString("iptables -t nat -A " + ChainTCP + " -p tcp -j REDIRECT --to-ports " + fmt.Sprint(TCPRedirectPort) + "\n\n")

	// Вставляем в начало PREROUTING
	for _, i := range []string{"br0", "br1", "br2", "nwg0", "nwg1", "wg0"} {
		sb.WriteString("iptables -t nat -D PREROUTING -i " + i + " -p tcp -j " + ChainTCP + " 2>/dev/null\n")
	}
	sb.WriteString("iptables -t nat -I PREROUTING 1 -i br0 -p tcp -j " + ChainTCP + "\n")
	sb.WriteString("iptables -t nat -I PREROUTING 1 -i nwg0 -p tcp -j " + ChainTCP + "\n")
	sb.WriteString("echo SB_TCP installed\n")

	return sb.String()
}

// buildMangleScript — скрипт для mangle PREROUTING (UDP TPROXY).
func buildMangleScript(vlessIPs []string) string {
	var sb strings.Builder
	sb.WriteString("#!/bin/sh\n")
	sb.WriteString("# UDP TPROXY → Sing-box\n\n")

	sb.WriteString("iptables -t mangle -N " + ChainUDP + " 2>/dev/null\n")
	sb.WriteString("iptables -t mangle -F " + ChainUDP + "\n")

	for _, cidr := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "127.0.0.0/8", "169.254.0.0/16",
		"172.16.0.0/12", "192.168.0.0/16", "224.0.0.0/4", "240.0.0.0/4",
	} {
		sb.WriteString("iptables -t mangle -A " + ChainUDP + " -d " + cidr + " -j RETURN\n")
	}

	for _, ip := range vlessIPs {
		if ip == "" {
			continue
		}
		sb.WriteString("iptables -t mangle -A " + ChainUDP + " -d " + ip + " -j RETURN\n")
	}

	sb.WriteString("iptables -t mangle -A " + ChainUDP + " -p udp -j TPROXY --on-port " + fmt.Sprint(UDPProxyPort) + " --tproxy-mark " + TProxyMark + "\n\n")

	for _, i := range []string{"br0", "br1", "br2", "nwg0", "nwg1", "wg0"} {
		sb.WriteString("iptables -t mangle -D PREROUTING -i " + i + " -p udp -j " + ChainUDP + " 2>/dev/null\n")
	}
	sb.WriteString("iptables -t mangle -A PREROUTING -i br0 -p udp -j " + ChainUDP + "\n")
	sb.WriteString("iptables -t mangle -A PREROUTING -i nwg0 -p udp -j " + ChainUDP + "\n\n")

	sb.WriteString("while ip rule del fwmark " + TProxyMark + " table " + TProxyTable + " 2>/dev/null; do :; done\n")
	sb.WriteString("ip rule add fwmark " + TProxyMark + " table " + TProxyTable + " 2>/dev/null\n")
	sb.WriteString("ip route add local default dev lo table " + TProxyTable + " 2>/dev/null\n")
	sb.WriteString("echo SB_UDP_TRANSIT installed\n")

	return sb.String()
}

// runScript — запускает shell-скрипт.
func runScript(path string) error {
	cmd := exec.Command("/bin/sh", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, string(out))
	}
	if len(out) > 0 {
		fmt.Printf("[iptables] %s: %s", path, string(out))
	}
	return nil
}

// run — запускает команду, игнорируя ошибки.
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
			strings.Contains(msg, "already exist") {
			return
		}
		fmt.Printf("[iptables] %s %v: %v (%s)\n", name, args, err, strings.TrimSpace(msg))
	}
}
