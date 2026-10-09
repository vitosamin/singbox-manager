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
	iptablesNatPath     = "/opt/etc/sing-box/.iptables_nat.sh"
	iptablesManglePath  = "/opt/etc/sing-box/.iptables_mangle.sh"
	ndmsHookPath        = "/opt/etc/ndm/netfilter.d/50-singbox.sh"
	tproxyInitPath      = "/opt/etc/init.d/S55singbox-tproxy.sh"
	singboxInitPath     = "/opt/etc/init.d/S99singbox.sh"
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

	// Init-скрипты (создаём ДО NDMS-хука — хук ссылается на S55singbox-tproxy.sh)
	if err := WriteInitScripts(); err != nil {
		fmt.Printf("[iptables] предупреждение: init scripts: %v\n", err)
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
	run("ip", "rule", "del", "fwmark", TProxyMark, "table", TProxyTable)
	run("ip", "route", "del", "local", "default", "dev", "lo", "table", TProxyTable)

	// NDMS hook
	_ = os.Remove(ndmsHookPath)

	return nil
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

# Sing-box должен быть запущен
ps -w | grep -v grep | grep -q "sing-box" || exit 0

# Небольшая задержка — даём NDM закончить перестройку
sleep 1

# Делегируем работу init-скрипту (он всегда на месте и идемпотентен)
if [ -x /opt/etc/init.d/S55singbox-tproxy.sh ]; then
    /opt/etc/init.d/S55singbox-tproxy.sh restart >/dev/null 2>&1
fi

logger -t singbox-ndm "netfilter rules restored (type=$type table=$table)"
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

// WriteInitScripts — создаёт init-скрипты для автозапуска.
func WriteInitScripts() error {
	// S55singbox-tproxy.sh — применяет iptables
	tproxyScript := `#!/bin/sh
# Применяет iptables-правила для Sing-box TProxy + REDIRECT

case "$1" in
    start)
        echo "Applying Sing-box iptables rules..."
        insmod /lib/modules/$(uname -r)/xt_TPROXY.ko 2>/dev/null
        insmod /lib/modules/$(uname -r)/xt_socket.ko 2>/dev/null
        [ -x ` + iptablesNatPath + ` ] && ` + iptablesNatPath + `
        [ -x ` + iptablesManglePath + ` ] && ` + iptablesManglePath + `
        echo "OK"
        ;;
    stop)
        echo "Removing Sing-box iptables rules..."
        for i in br0 br1 br2 nwg0 nwg1 wg0; do
            iptables -t nat -D PREROUTING -i $i -p tcp -j ` + ChainTCP + ` 2>/dev/null
            iptables -t mangle -D PREROUTING -i $i -p udp -j ` + ChainUDP + ` 2>/dev/null
        done
        iptables -t nat -F ` + ChainTCP + ` 2>/dev/null
        iptables -t nat -X ` + ChainTCP + ` 2>/dev/null
        iptables -t mangle -F ` + ChainUDP + ` 2>/dev/null
        iptables -t mangle -X ` + ChainUDP + ` 2>/dev/null
        ip rule del fwmark ` + TProxyMark + ` table ` + TProxyTable + ` 2>/dev/null
        ip route del local default dev lo table ` + TProxyTable + ` 2>/dev/null
        echo "OK"
        ;;
    restart)
        $0 stop
        sleep 1
        $0 start
        ;;
    status)
        iptables -t nat -L ` + ChainTCP + ` -n 2>/dev/null && echo "TCP: OK" || echo "TCP: NOT SET"
        iptables -t mangle -L ` + ChainUDP + ` -n 2>/dev/null && echo "UDP: OK" || echo "UDP: NOT SET"
        ;;
    *)
        echo "Usage: $0 {start|stop|restart|status}"
        exit 1
        ;;
esac
exit 0
`
	if err := os.WriteFile(tproxyInitPath, []byte(tproxyScript), 0755); err != nil {
		return fmt.Errorf("write tproxy init: %w", err)
	}
	fmt.Println("[iptables] init script:", tproxyInitPath)

	// S99singbox.sh — запускает Sing-box
	singboxScript := `#!/bin/sh
# Запуск Sing-box для TProxy + REDIRECT

BIN=/opt/etc/sing-box/sing-box
CONFIG=/opt/etc/sing-box/config.json
LOADER=/opt/lib/ld-linux-aarch64.so.1
LOGFILE=/opt/var/log/sing-box.log
PIDFILE=/opt/var/run/sing-box.pid

case "$1" in
    start)
        if [ -f "$PIDFILE" ] && kill -0 $(cat "$PIDFILE") 2>/dev/null; then
            echo "sing-box уже запущен"
            exit 0
        fi
        echo "Запуск sing-box..."
        mkdir -p /opt/var/log /opt/var/run
        $LOADER $BIN run -c $CONFIG > $LOGFILE 2>&1 &
        echo $! > $PIDFILE
        sleep 2
        if kill -0 $(cat "$PIDFILE") 2>/dev/null; then
            echo "OK"
        else
            echo "ОШИБКА. Лог:"
            cat $LOGFILE
            exit 1
        fi
        ;;
    stop)
        echo "Остановка sing-box..."
        if [ -f "$PIDFILE" ]; then
            kill $(cat "$PIDFILE") 2>/dev/null
            rm -f "$PIDFILE"
        fi
        killall sing-box 2>/dev/null
        echo "OK"
        ;;
    restart)
        $0 stop
        sleep 1
        $0 start
        ;;
    status)
        if [ -f "$PIDFILE" ] && kill -0 $(cat "$PIDFILE") 2>/dev/null; then
            echo "sing-box работает (PID $(cat $PIDFILE))"
        else
            echo "sing-box не запущен"
        fi
        ;;
    *)
        echo "Usage: $0 {start|stop|restart|status}"
        exit 1
        ;;
esac
exit 0
`
	if err := os.WriteFile(singboxInitPath, []byte(singboxScript), 0755); err != nil {
		return fmt.Errorf("write singbox init: %w", err)
	}
	fmt.Println("[iptables] init script:", singboxInitPath)

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

	sb.WriteString("iptables -t nat -A " + ChainTCP + " -p tcp -j REDIRECT --to-ports 1082\n\n")

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

	sb.WriteString("iptables -t mangle -A " + ChainUDP + " -p udp -j TPROXY --on-port 1081 --tproxy-mark " + TProxyMark + "\n\n")

	for _, i := range []string{"br0", "br1", "br2", "nwg0", "nwg1", "wg0"} {
		sb.WriteString("iptables -t mangle -D PREROUTING -i " + i + " -p udp -j " + ChainUDP + " 2>/dev/null\n")
	}
	sb.WriteString("iptables -t mangle -A PREROUTING -i br0 -p udp -j " + ChainUDP + "\n")
	sb.WriteString("iptables -t mangle -A PREROUTING -i nwg0 -p udp -j " + ChainUDP + "\n\n")

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
