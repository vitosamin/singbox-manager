// Package iptables управляет iptables-правилами для прозрачного проксирования
// трафика через sing-box (TProxy + REDIRECT).
package iptables

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	ChainTCP = "SB_TCP"
	ChainUDP = "SB_UDP_TRANSIT"

	TCPRedirectPort = 1082
	UDPProxyPort    = 1081

	TProxyMark  = "1"
	TProxyTable = "100"

	iptablesNatPath    = "/opt/etc/sing-box/.iptables_nat.sh"
	iptablesManglePath = "/opt/etc/sing-box/.iptables_mangle.sh"
	ndmsHookPath       = "/opt/etc/ndm/netfilter.d/50-singbox.sh"
	tproxyInitPath     = "/opt/etc/init.d/S55singbox-tproxy.sh"
)

// DetectWANIP — определяет публичный IP роутера (WAN).
// Сначала через `ip route get`, потом через ndmc.
func DetectWANIP() string {
	// Способ 1: ip route get 8.8.8.8 → "8.8.8.8 via X.X.X.1 dev ppp0 src X.X.X.X"
	if ip := detectWANIPRoute(); ip != "" {
		return ip
	}
	// Способ 2: ndmc show interface PPPoE0
	if ip := detectWANIPNdmc(); ip != "" {
		return ip
	}
	return ""
}

func detectWANIPRoute() string {
	cmd := exec.Command("ip", "route", "get", "8.8.8.8")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(out))
	for i, f := range fields {
		if f == "src" && i+1 < len(fields) {
			ip := fields[i+1]
			if strings.Count(ip, ".") == 3 {
				return ip
			}
		}
	}
	return ""
}

func detectWANIPNdmc() string {
	cmd := exec.Command("ndmc", "-c", "show interface PPPoE0")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "address:") {
			ip := strings.TrimSpace(strings.TrimPrefix(line, "address:"))
			if strings.Count(ip, ".") == 3 {
				return ip
			}
		}
	}
	return ""
}

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

// Setup устанавливает iptables-правила.
func Setup(vlessIPs []string) error {
	// Автоопределение WAN IP
	wanIP := DetectWANIP()
	excludeIPs := append([]string{}, vlessIPs...)
	if wanIP != "" {
		excludeIPs = append(excludeIPs, wanIP)
		fmt.Printf("[iptables] WAN IP: %s\n", wanIP)
	}

	fmt.Printf("[iptables] Setup: TProxy+REDIRECT (exclude: %v)\n", excludeIPs)

	loadKernelModules()

	natScript := buildNatScript(excludeIPs)
	mangleScript := buildMangleScript(excludeIPs)

	_ = os.MkdirAll("/opt/etc/sing-box", 0755)

	if err := os.WriteFile(iptablesNatPath, []byte(natScript), 0755); err != nil {
		return fmt.Errorf("write nat script: %w", err)
	}
	if err := os.WriteFile(iptablesManglePath, []byte(mangleScript), 0755); err != nil {
		return fmt.Errorf("write mangle script: %w", err)
	}

	if err := runScript(iptablesNatPath); err != nil {
		return fmt.Errorf("apply nat: %w", err)
	}
	if err := runScript(iptablesManglePath); err != nil {
		return fmt.Errorf("apply mangle: %w", err)
	}

	if err := WriteTProxyInit(); err != nil {
		fmt.Printf("[iptables] предупреждение: tproxy init: %v\n", err)
	}
	if err := WriteNDMSHook(); err != nil {
		fmt.Printf("[iptables] предупреждение: NDMS hook: %v\n", err)
	}

	fmt.Println("[iptables] правила TProxy+REDIRECT установлены")
	return nil
}

func Cleanup() error {
	fmt.Println("[iptables] Cleanup")
	interfaces := []string{"br0", "br1", "br2", "nwg0", "nwg1", "wg0"}

	for _, i := range interfaces {
		run("iptables", "-t", "nat", "-D", "PREROUTING", "-i", i, "-p", "tcp", "-j", ChainTCP)
	}
	run("iptables", "-t", "nat", "-F", ChainTCP)
	run("iptables", "-t", "nat", "-X", ChainTCP)

	for _, i := range interfaces {
		run("iptables", "-t", "mangle", "-D", "PREROUTING", "-i", i, "-p", "udp", "-j", ChainUDP)
	}
	run("iptables", "-t", "mangle", "-F", ChainUDP)
	run("iptables", "-t", "mangle", "-X", ChainUDP)

	for {
		cmd := exec.Command("ip", "rule", "del", "fwmark", TProxyMark, "table", TProxyTable)
		if err := cmd.Run(); err != nil {
			break
		}
	}
	run("ip", "route", "del", "local", "default", "dev", "lo", "table", TProxyTable)
	_ = os.Remove(ndmsHookPath)
	return nil
}

func CheckChain(chain, table string) error {
	cmd := exec.Command("iptables", "-t", table, "-L", chain, "-n")
	return cmd.Run()
}

func WriteNDMSHook() error {
	script := `#!/bin/sh
type="$1"
table="$2"

[ "$type" != "iptables" ] && exit 0
[ "$table" != "nat" ] && [ "$table" != "mangle" ] && exit 0
[ -f /opt/etc/sing-box/vpn.disabled ] && exit 0

ps -w | grep -v grep | grep -q "sing-box" || exit 0

for i in 1 2 3 4 5; do
    nat_ok=0
    mangle_ok=0
    iptables -t nat -L SB_TCP -n >/dev/null 2>&1 && nat_ok=1
    iptables -t mangle -L SB_UDP_TRANSIT -n >/dev/null 2>&1 && mangle_ok=1

    if [ "$nat_ok" = "1" ] && [ "$mangle_ok" = "1" ]; then
        logger -t singbox-ndm "netfilter rules OK (attempt=$i)"
        exit 0
    fi

    sleep 2
    if [ -x /opt/etc/init.d/S55singbox-tproxy.sh ]; then
        /opt/etc/init.d/S55singbox-tproxy.sh start >/dev/null 2>&1
    fi
done

logger -t singbox-ndm "netfilter rules NOT restored after 5 attempts"
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

func WriteTProxyInit() error {
	script := `#!/bin/sh
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

func buildNatScript(excludeIPs []string) string {
	var sb strings.Builder
	sb.WriteString("#!/bin/sh\n# TCP REDIRECT → Sing-box\n\n")
	sb.WriteString("iptables -t nat -N " + ChainTCP + " 2>/dev/null\n")
	sb.WriteString("iptables -t nat -F " + ChainTCP + "\n")

	for _, cidr := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "127.0.0.0/8", "169.254.0.0/16",
		"172.16.0.0/12", "192.168.0.0/16", "224.0.0.0/4", "240.0.0.0/4",
	} {
		sb.WriteString("iptables -t nat -A " + ChainTCP + " -d " + cidr + " -j RETURN\n")
	}
	for _, ip := range excludeIPs {
		if ip == "" {
			continue
		}
		sb.WriteString("iptables -t nat -A " + ChainTCP + " -d " + ip + " -j RETURN\n")
	}
	sb.WriteString("iptables -t nat -A " + ChainTCP + " -p tcp -j REDIRECT --to-ports " + fmt.Sprint(TCPRedirectPort) + "\n\n")

	for _, i := range []string{"br0", "br1", "br2", "nwg0", "nwg1", "wg0"} {
		sb.WriteString("iptables -t nat -D PREROUTING -i " + i + " -p tcp -j " + ChainTCP + " 2>/dev/null\n")
	}
	sb.WriteString("iptables -t nat -I PREROUTING 1 -i br0 -p tcp -j " + ChainTCP + "\n")
	sb.WriteString("iptables -t nat -I PREROUTING 1 -i nwg0 -p tcp -j " + ChainTCP + "\n")
	sb.WriteString("echo SB_TCP installed\n")
	return sb.String()
}

func buildMangleScript(excludeIPs []string) string {
	var sb strings.Builder
	sb.WriteString("#!/bin/sh\n# UDP TPROXY → Sing-box\n\n")
	sb.WriteString("iptables -t mangle -N " + ChainUDP + " 2>/dev/null\n")
	sb.WriteString("iptables -t mangle -F " + ChainUDP + "\n")

	for _, cidr := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "127.0.0.0/8", "169.254.0.0/16",
		"172.16.0.0/12", "192.168.0.0/16", "224.0.0.0/4", "240.0.0.0/4",
	} {
		sb.WriteString("iptables -t mangle -A " + ChainUDP + " -d " + cidr + " -j RETURN\n")
	}
	for _, ip := range excludeIPs {
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
