#!/bin/sh
# Установщик Sing-box Manager v4.3.8 для Keenetic (Entware).

set -e

REPO="vitosamin/singbox-manager"
RELEASE_TAG="v4.4.4"
MANAGER_BIN_PATH="/opt/bin/singbox-manager"
SINGBOX_BIN_PATH="/opt/etc/sing-box/sing-box"
INIT_PATH="/opt/etc/init.d/S99singbox-manager"
TPROXY_INIT_PATH="/opt/etc/init.d/S55singbox-tproxy.sh"
NDMS_HOOK_PATH="/opt/etc/ndm/netfilter.d/50-singbox.sh"
WATCHDOG_PATH="/opt/bin/singbox-watchdog.sh"
MONITOR_PATH="/opt/bin/singbox-monitor.sh"
DATA_DIR="/opt/etc/sing-box"
CONFIG_D="${DATA_DIR}/config.d"
LOGDIR="/tmp/singbox-logs"
TMP_DIR="/tmp"
MARKER="/opt/etc/sing-box/vpn.disabled"

# Cron-watchdog: 0 = отключён (по умолчанию), 1 = включён
ENABLE_CRON_WATCHDOG=1

if [ ! -d /opt/etc ]; then
    echo "ОШИБКА: Entware не установлен."
    exit 1
fi

# ============================================================
# 1. Зависимости
# ============================================================
echo ""
echo "==> Устанавливаем зависимости (iptables, ipset, wget, curl)"
opkg update >/dev/null 2>&1 || true

install_pkg() {
    PKG="$1"
    if opkg list-installed 2>/dev/null | grep -q "^${PKG} "; then
        echo "    [OK] $PKG уже установлен"
    else
        echo "    [..] Устанавливаем $PKG"
        opkg install "$PKG" >/dev/null 2>&1 || \
            opkg install "$PKG" 2>&1 | tail -3
    fi
}

install_pkg iptables
install_pkg ipset
install_pkg libipset
install_pkg libmnl
install_pkg wget
install_pkg wget-nossl
install_pkg curl
install_pkg ca-bundle
install_pkg ca-certificates
install_pkg cron

if ! command -v iptables >/dev/null 2>&1; then
    echo "ОШИБКА: iptables не установился."
    exit 1
fi

# ============================================================
# 2. Каталог логов в RAM
# ============================================================
mkdir -p "$LOGDIR"
# Симлинк для UI-менеджера: он читает /opt/var/log/sing-box.log
mkdir -p /opt/var/log
ln -sf "$LOGDIR/sing-box.log" /opt/var/log/sing-box.log
echo "    [OK] Логи в RAM: $LOGDIR"
echo "    [OK] Симлинк: /opt/var/log/sing-box.log -> $LOGDIR/sing-box.log"

# ============================================================
# 3. Модули ядра TPROXY
# ============================================================
KERNEL=$(uname -r)
HAS_TPROXY=0
for p in "/lib/modules/$KERNEL/xt_TPROXY.ko" "/lib/system-modules/xt_TPROXY.ko" \
         "/lib/modules/$KERNEL/xt_socket.ko" "/lib/system-modules/xt_socket.ko"; do
    [ -f "$p" ] && HAS_TPROXY=$((HAS_TPROXY+1))
done

if [ "$HAS_TPROXY" -lt 2 ]; then
    echo ""
    echo "WARN: модули xt_TPROXY / xt_socket не найдены!"
    echo "      Установи компонент 'Модули ядра для Netfilter' в KeeneticOS."
    echo ""
fi

for mod in xt_TPROXY xt_socket xt_nat xt_REDIRECT ip_set xt_set; do
    insmod "$mod" 2>/dev/null || true
done

# ============================================================
# 4. Архитектура
# ============================================================
ARCH=$(uname -m)
case "$ARCH" in
    aarch64|arm64)
        MANAGER_BIN="singbox-manager-arm64"
        SINGBOX_ARCH="linux-arm64"
        ;;
    mips|mips32|mipsel)
        MANAGER_BIN="singbox-manager-mipsle"
        SINGBOX_ARCH="linux-mipsle-softfloat"
        ;;
    *)
        echo "ОШИБКА: неподдерживаемая архитектура: $ARCH"
        exit 1
        ;;
esac

echo ""
echo "=== Sing-box Manager $RELEASE_TAG Installer ==="
echo "Архитектура: $ARCH -> $MANAGER_BIN (Sing-box: $SINGBOX_ARCH)"

LOADER=""
for candidate in /opt/lib/ld-linux*.so* /opt/lib/ld-*.so*; do
    if [ -f "$candidate" ]; then
        LOADER="$candidate"
        break
    fi
done
[ -z "$LOADER" ] && LOADER="/opt/lib/ld-linux.so.3"
echo "Загрузчик: $LOADER"

# ============================================================
# 5. Остановка старого
# ============================================================
if [ -x "$INIT_PATH" ]; then
    "$INIT_PATH" stop 2>/dev/null || true
fi
killall singbox-manager 2>/dev/null || true
killall sing-box 2>/dev/null || true
killall singbox-monitor 2>/dev/null || true

mkdir -p "$DATA_DIR" "$CONFIG_D" /opt/var/run /opt/var/log

# ============================================================
# 6. Менеджер
# ============================================================
echo ""
echo "==> Скачиваем менеджер: $MANAGER_BIN"
cd "$TMP_DIR"
URL="https://github.com/$REPO/releases/download/$RELEASE_TAG/$MANAGER_BIN"

if command -v curl >/dev/null 2>&1; then
    curl -L -o singbox-manager.new "$URL"
else
    wget -O singbox-manager.new "$URL"
fi

[ ! -f singbox-manager.new ] && { echo "ОШИБКА: менеджер не скачался"; exit 1; }

chmod +x singbox-manager.new
mv singbox-manager.new "$MANAGER_BIN_PATH"
chmod +x "$MANAGER_BIN_PATH"
echo "    OK: $MANAGER_BIN_PATH"

# ============================================================
# 7. Sing-box с SagerNet
# ============================================================
if [ ! -f "$SINGBOX_BIN_PATH" ]; then
    echo ""
    echo "==> Скачиваем официальный Sing-box 1.14.2 (SagerNet, $SINGBOX_ARCH)"
    SINGBOX_URL="https://github.com/SagerNet/sing-box/releases/download/v1.14.2/sing-box-1.14.2-${SINGBOX_ARCH}.tar.gz"

    if command -v curl >/dev/null 2>&1; then
        curl -L -o sing-box.tar.gz "$SINGBOX_URL"
    else
        wget -O sing-box.tar.gz "$SINGBOX_URL"
    fi

    [ ! -f sing-box.tar.gz ] && { echo "ОШИБКА: sing-box не скачался"; exit 1; }

    SIZE=$(wc -c < sing-box.tar.gz)
    if [ "$SIZE" -lt 100000 ]; then
        echo "ОШИБКА: слишком маленький файл ($SIZE байт), вероятно 404"
        rm -f sing-box.tar.gz
        exit 1
    fi

    tar xzf sing-box.tar.gz
    SINGBOX_DIR="sing-box-1.14.2-${SINGBOX_ARCH}"
    cp "$SINGBOX_DIR/sing-box" "$SINGBOX_BIN_PATH"
    cp "$SINGBOX_DIR/libcronet.so" "$DATA_DIR/" 2>/dev/null || true
    chmod +x "$SINGBOX_BIN_PATH"

    rm -rf sing-box.tar.gz "$SINGBOX_DIR"
    echo "    OK: $SINGBOX_BIN_PATH"
else
    echo ""
    echo "==> Sing-box уже установлен: $SINGBOX_BIN_PATH"
fi

# ============================================================
# 8. Init-скрипт менеджера
# ============================================================
echo ""
echo "==> Устанавливаем init-скрипт: $INIT_PATH"
cat > "$INIT_PATH" << INIT_EOF
#!/bin/sh
BIN=/opt/bin/singbox-manager
LOGFILE=/tmp/singbox-logs/manager.log
ADDR=":9091"
MARKER=/opt/etc/sing-box/vpn.disabled
SINGBOX=/opt/etc/sing-box/sing-box
LOADER=$LOADER
CONFIG=/opt/etc/sing-box/config.d
TPROXY_INIT=$TPROXY_INIT_PATH
MONITOR=$MONITOR_PATH
LOGDIR=$LOGDIR

ensure_logdir() {
    [ -d "\$LOGDIR" ] || mkdir -p "\$LOGDIR"
    # Восстанавливаем симлинк для UI (на случай, если /tmp очистился)
    [ -f "\$LOGDIR/sing-box.log" ] || touch "\$LOGDIR/sing-box.log"
    mkdir -p /opt/var/log
    ln -sf "\$LOGDIR/sing-box.log" /opt/var/log/sing-box.log
}

start_monitor() {
    if ! ps -w | grep -v grep | grep -q singbox-monitor; then
        ensure_logdir
        \$MONITOR </dev/null >/dev/null 2>&1 &
    fi
}

apply_tproxy() {
    if [ -x "\$TPROXY_INIT" ]; then
        if ! iptables -t nat -C PREROUTING -j SB_TCP >/dev/null 2>&1 || \
           ! iptables -t mangle -C PREROUTING -p udp -j SB_UDP_TRANSIT >/dev/null 2>&1; then
            echo "Применение iptables-правил..."
            "\$TPROXY_INIT" start >/dev/null 2>&1 || true
        fi
    fi
}

case "\$1" in
    start)
        ensure_logdir

        if ps -w | grep -v grep | grep -q "\$BIN"; then
            echo "singbox-manager уже запущен"
        else
            echo "Запуск singbox-manager..."
            \$BIN -addr \$ADDR > \$LOGFILE 2>&1 &
            sleep 3
            if ! ps -w | grep -v grep | grep -q "\$BIN"; then
                echo "ОШИБКА. Лог:"; cat \$LOGFILE; exit 1
            fi
            echo "OK"
        fi

        if [ -d "\$CONFIG" ] && [ ! -f "\$MARKER" ]; then
            if ! ps -w | grep -v grep | grep -q "sing-box run"; then
                echo "Автозапуск sing-box..."
                \$SINGBOX run -C \$CONFIG > /tmp/singbox-logs/sing-box.log 2>&1 &
                sleep 5
            fi
            apply_tproxy
        fi

        start_monitor
        ;;
    stop)
        echo "Остановка..."
        killall singbox-monitor 2>/dev/null
        killall singbox-manager 2>/dev/null
        killall sing-box 2>/dev/null
        echo "OK"
        ;;
    restart) \$0 stop; sleep 2; \$0 start ;;
    status)
        ps -w | grep -v grep | grep -q "\$BIN" && echo "singbox-manager работает" || echo "singbox-manager не запущен"
        ps -w | grep -v grep | grep -q "sing-box run" && echo "sing-box работает" || echo "sing-box не запущен"
        ps -w | grep -v grep | grep -q singbox-monitor && echo "monitor работает" || echo "monitor не запущен"
        ;;
    *) echo "Использование: \$0 {start|stop|restart|status}"; exit 1 ;;
esac
exit 0
INIT_EOF

chmod +x "$INIT_PATH"

# ============================================================
# 9. TPROXY init-скрипт (TCP→REDIRECT, UDP→TPROXY)
# ============================================================
echo ""
echo "==> Устанавливаем TPROXY-скрипт: $TPROXY_INIT_PATH"
cat > "$TPROXY_INIT_PATH" << 'TPROXY_EOF'
#!/bin/sh
# Sing-box: TCP → REDIRECT :1082 (nat), UDP → TPROXY :1081 (mangle)
TPROXY_PORT=1081
REDIRECT_PORT=1082
FW_MARK=0x1
ROUTE_TABLE=100

for mod in xt_TPROXY xt_socket xt_nat xt_REDIRECT ip_set xt_set; do
    insmod "$mod" 2>/dev/null || true
done

apply_rules() {
    # Удаляем все старые ссылки из PREROUTING
    while iptables -t nat -D PREROUTING -j SB_TCP 2>/dev/null; do :; done
    while iptables -t nat -D PREROUTING -i br0 -j SB_TCP 2>/dev/null; do :; done
    while iptables -t nat -D PREROUTING -i nwg0 -j SB_TCP 2>/dev/null; do :; done
    while iptables -t mangle -D PREROUTING -p udp -j SB_UDP_TRANSIT 2>/dev/null; do :; done
    while iptables -t mangle -D PREROUTING -p tcp -j SB_TCP_TPROXY 2>/dev/null; do :; done

    # Удаляем старую mangle-TCP-цепочку (от экспериментов)
    iptables -t mangle -F SB_TCP_TPROXY 2>/dev/null || true
    iptables -t mangle -X SB_TCP_TPROXY 2>/dev/null || true

    # --- TCP (nat REDIRECT) ---
    iptables -t nat -N SB_TCP 2>/dev/null || true
    iptables -t nat -F SB_TCP
    for net in 0.0.0.0/8 10.0.0.0/8 127.0.0.0/8 172.16.0.0/12 192.168.0.0/16 224.0.0.0/4 255.255.255.255/32; do
        iptables -t nat -A SB_TCP -d "$net" -j RETURN
    done
    iptables -t nat -A SB_TCP -p tcp -m mark --mark "$FW_MARK" -j RETURN
    iptables -t nat -A SB_TCP -p tcp -j REDIRECT --to-ports "$REDIRECT_PORT"
    iptables -t nat -I PREROUTING 1 -j SB_TCP

    # --- UDP (mangle TPROXY) ---
    iptables -t mangle -N SB_UDP_TRANSIT 2>/dev/null || true
    iptables -t mangle -F SB_UDP_TRANSIT
    for net in 0.0.0.0/8 10.0.0.0/8 127.0.0.0/8 172.16.0.0/12 192.168.0.0/16 224.0.0.0/4 255.255.255.255/32; do
        iptables -t mangle -A SB_UDP_TRANSIT -d "$net" -j RETURN
    done
    iptables -t mangle -A SB_UDP_TRANSIT -p udp --dport 53 -j RETURN
    iptables -t mangle -A SB_UDP_TRANSIT -p udp --dport 67:68 -j RETURN
    iptables -t mangle -A SB_UDP_TRANSIT -p udp -m mark --mark "$FW_MARK" -j RETURN
    iptables -t mangle -A SB_UDP_TRANSIT -p udp -j TPROXY --on-port "$TPROXY_PORT" --tproxy-mark "$FW_MARK"
    iptables -t mangle -I PREROUTING 1 -p udp -j SB_UDP_TRANSIT

    # --- Policy routing ---
    ip rule del fwmark "$FW_MARK" lookup "$ROUTE_TABLE" 2>/dev/null || true
    ip rule add fwmark "$FW_MARK" lookup "$ROUTE_TABLE"
    ip route flush table "$ROUTE_TABLE" 2>/dev/null || true
    ip route add local default dev lo table "$ROUTE_TABLE"

    # Опционально: сброс conntrack (Keenetic)
    if [ -w /proc/sys/net/netfilter/nf_conntrack_sweep ]; then
        echo 1 > /proc/sys/net/netfilter/nf_conntrack_sweep 2>/dev/null || true
    fi
}

remove_rules() {
    while iptables -t nat -D PREROUTING -j SB_TCP 2>/dev/null; do :; done
    while iptables -t mangle -D PREROUTING -p udp -j SB_UDP_TRANSIT 2>/dev/null; do :; done
    iptables -t nat -F SB_TCP 2>/dev/null || true
    iptables -t mangle -F SB_UDP_TRANSIT 2>/dev/null || true
    ip rule del fwmark "$FW_MARK" lookup "$ROUTE_TABLE" 2>/dev/null || true
    ip route flush table "$ROUTE_TABLE" 2>/dev/null || true
}

case "$1" in
    start)
        apply_rules
        echo "SB_TCP installed"
        echo "SB_UDP_TRANSIT installed"
        echo "OK: TCP -> REDIRECT :$REDIRECT_PORT, UDP -> TPROXY :$TPROXY_PORT"
        ;;
    stop)
        remove_rules
        echo "OK: правила удалены"
        ;;
    restart)
        $0 stop; sleep 1; $0 start
        ;;
    status)
        iptables -t nat -C PREROUTING -j SB_TCP >/dev/null 2>&1 && echo "SB_TCP: активны" || echo "SB_TCP: отсутствуют"
        iptables -t mangle -C PREROUTING -p udp -j SB_UDP_TRANSIT >/dev/null 2>&1 && echo "SB_UDP_TRANSIT: активны" || echo "SB_UDP_TRANSIT: отсутствуют"
        ;;
    *)
        # Обратная совместимость: без аргумента делаем start
        apply_rules
        echo "OK: TCP -> REDIRECT :$REDIRECT_PORT, UDP -> TPROXY :$TPROXY_PORT"
        ;;
esac
exit 0
TPROXY_EOF

chmod +x "$TPROXY_INIT_PATH"

# ============================================================
# 10. NDMS-хук (с защитой от цикла)
# ============================================================
echo ""
echo "==> Устанавливаем NDMS-хук: $NDMS_HOOK_PATH"
mkdir -p /opt/etc/ndm/netfilter.d/
cat > "$NDMS_HOOK_PATH" << 'HOOK_EOF'
#!/bin/sh
[ "$table" = "nat" ] || [ "$table" = "mangle" ] || exit 0

LOGDIR=/tmp/singbox-logs
LOGFILE=$LOGDIR/hook.log
MARKER=/opt/etc/sing-box/vpn.disabled
TPROXY_INIT=/opt/etc/init.d/S55singbox-tproxy.sh

[ -d "$LOGDIR" ] || mkdir -p "$LOGDIR"
[ -f "$MARKER" ] && exit 0
[ ! -x "$TPROXY_INIT" ] && exit 0

# Защита от цикла: если цепочки уже стоят — выходим молча
NEED_RESTORE=0
iptables -t nat -C PREROUTING -j SB_TCP >/dev/null 2>&1 || NEED_RESTORE=1
iptables -t mangle -C PREROUTING -p udp -j SB_UDP_TRANSIT >/dev/null 2>&1 || NEED_RESTORE=1

if [ "$NEED_RESTORE" -eq 0 ]; then
    exit 0
fi

echo "$(date '+%Y-%m-%d %H:%M:%S') цепочки пропали (table=$table) → восстанавливаем" >> "$LOGFILE"

(
    sleep 2
    "$TPROXY_INIT" start >/dev/null 2>&1 || true
    echo "$(date '+%Y-%m-%d %H:%M:%S') восстановлены (table=$table)" >> "$LOGFILE"
) &

[ -f "$LOGFILE" ] && tail -500 "$LOGFILE" > "$LOGFILE.tmp" && mv "$LOGFILE.tmp" "$LOGFILE"

exit 0
HOOK_EOF

chmod +x "$NDMS_HOOK_PATH"

# ============================================================
# 11. Watchdog (RAM)
# ============================================================
echo ""
echo "==> Устанавливаем watchdog: $WATCHDOG_PATH"
cat > "$WATCHDOG_PATH" << 'WD_EOF'
#!/bin/sh
LOGDIR=/tmp/singbox-logs
LOG=$LOGDIR/watchdog.log
MARKER=/opt/etc/sing-box/vpn.disabled

[ -d "$LOGDIR" ] || mkdir -p "$LOGDIR"
[ -f "$MARKER" ] && exit 0
NOW=$(date '+%Y-%m-%d %H:%M:%S')

if ! ps -w | grep -v grep | grep -q "/opt/bin/singbox-manager"; then
    echo "$NOW: singbox-manager упал → перезапуск" >> "$LOG"
    /opt/etc/init.d/S99singbox-manager start >/dev/null 2>&1
fi

# 1. sing-box мёртв → сначала снять правила (защита от black hole)
if ! ps -w | grep -v grep | grep -q "sing-box run"; then
    if iptables -t nat -S PREROUTING 2>/dev/null | grep -q 'SB_TCP' || \
       iptables -t mangle -S PREROUTING 2>/dev/null | grep -q 'SB_UDP_TRANSIT'; then
        echo "$NOW: sing-box мёртв → снимаю правила" >> "$LOG"
        /opt/etc/init.d/S55singbox-tproxy.sh stop >/dev/null 2>&1 || true
    fi
    echo "$NOW: sing-box мёртв → перезапуск S99" >> "$LOG"
    /opt/etc/init.d/S99singbox-manager start >/dev/null 2>&1
    [ -f "$LOG" ] && tail -500 "$LOG" > "$LOG.tmp" && mv "$LOG.tmp" "$LOG"
    exit 0
fi

if ! iptables -t nat -C PREROUTING -j SB_TCP >/dev/null 2>&1; then
    echo "$NOW: SB_TCP пропал → восстановление" >> "$LOG"
    /opt/etc/init.d/S55singbox-tproxy.sh start >/dev/null 2>&1
fi

if ! iptables -t mangle -C PREROUTING -p udp -j SB_UDP_TRANSIT >/dev/null 2>&1; then
    echo "$NOW: SB_UDP_TRANSIT пропал → восстановление" >> "$LOG"
    /opt/etc/init.d/S55singbox-tproxy.sh start >/dev/null 2>&1
fi

[ -f "$LOG" ] && tail -500 "$LOG" > "$LOG.tmp" && mv "$LOG.tmp" "$LOG"
exit 0
WD_EOF

chmod +x "$WATCHDOG_PATH"

# ============================================================
# 12. Monitor (RAM)
# ============================================================
echo ""
echo "==> Устанавливаем мониторинг: $MONITOR_PATH"
cat > "$MONITOR_PATH" << 'MON_EOF'
#!/bin/sh
LOGDIR=/tmp/singbox-logs
LOG=$LOGDIR/monitor.log
[ -d "$LOGDIR" ] || mkdir -p "$LOGDIR"

while true; do
    TS=$(date "+%Y-%m-%d %H:%M:%S")
    TCP_N=$(iptables -t nat -S PREROUTING 2>/dev/null | grep -c 'SB_TCP'); [ "$TCP_N" -gt 0 ] && TCP=1 || TCP=0
    UDP=$(iptables -t mangle -C PREROUTING -p udp -j SB_UDP_TRANSIT >/dev/null 2>&1 && echo 1 || echo 0)
    RULE=$(ip rule show 2>/dev/null | grep -c "fwmark 0x1")
    echo "$TS TCP=$TCP UDP=$UDP RULE=$RULE" >> "$LOG"
    [ -f "$LOG" ] && tail -2000 "$LOG" > "$LOG.tmp" && mv "$LOG.tmp" "$LOG"
    sleep 30
done
MON_EOF

chmod +x "$MONITOR_PATH"

# ============================================================
# 13. Cron-watchdog (опционально)
# ============================================================
if [ "$ENABLE_CRON_WATCHDOG" -eq 1 ]; then
    echo ""
    echo "==> Включаем cron-watchdog"
    mkdir -p /opt/etc/cron.d
    mkdir -p /opt/var/spool/cron/crontabs
    cat > /opt/var/spool/cron/crontabs/root << 'CRONTAB_EOF'
* * * * * /opt/bin/singbox-watchdog.sh
CRONTAB_EOF
    chmod 600 /opt/var/spool/cron/crontabs/root
    chown root:root /opt/var/spool/cron/crontabs/root

    if [ ! -x /opt/etc/init.d/S10cron ]; then
        cat > /opt/etc/init.d/S10cron << 'CRON_EOF'
#!/bin/sh
case "$1" in
    start)
        ps -w | grep -v grep | grep -q crond && echo "crond уже запущен" || {
            echo "Запуск crond..."
            crond -c /opt/var/spool/cron/crontabs -l 8
            sleep 1
            ps -w | grep -v grep | grep -q crond && echo "OK" || echo "FAIL"
        }
        ;;
    stop) killall crond 2>/dev/null && echo "OK" ;;
    restart) $0 stop; sleep 1; $0 start ;;
    status) ps -w | grep -v grep | grep -q crond && echo "crond работает" || echo "crond не запущен" ;;
    *) echo "Usage: $0 {start|stop|restart|status}"; exit 1 ;;
esac
exit 0
CRON_EOF
        chmod +x /opt/etc/init.d/S10cron
    fi
    /opt/etc/init.d/S10cron start >/dev/null 2>&1 || true
else
    echo ""
    echo "==> cron-watchdog отключён (защита: NDMS-хук + встроенный watchdog)"
fi

# ============================================================
# 14. Cron-бэкап
# ============================================================
CRONDAILY_DIR="/opt/etc/cron.daily"
if [ -d "$CRONDAILY_DIR" ] || mkdir -p "$CRONDAILY_DIR" 2>/dev/null; then
    cat > "$CRONDAILY_DIR/backup-state" << 'CRON_EOF'
#!/bin/sh
BACKUP_DIR="/opt/etc/sing-box/backups"
mkdir -p "$BACKUP_DIR"
DATE=$(date +%Y%m%d)
[ -f "/opt/etc/sing-box/state.json" ] && cp "/opt/etc/sing-box/state.json" "$BACKUP_DIR/state-$DATE.json"
[ -d "/opt/etc/sing-box/config.d" ] && tar czf "$BACKUP_DIR/config-$DATE.tar.gz" -C /opt/etc/sing-box config.d 2>/dev/null
find "$BACKUP_DIR" -name "state-*.json" -mtime +7 -delete 2>/dev/null
find "$BACKUP_DIR" -name "config-*.tar.gz" -mtime +7 -delete 2>/dev/null
exit 0
CRON_EOF
    chmod +x "$CRONDAILY_DIR/backup-state"
fi

# ============================================================
# 15. Запуск
# ============================================================
echo ""
echo "==> Запускаем менеджер..."
"$INIT_PATH" start

echo ""
echo "==> Применяем TPROXY-правила..."
"$TPROXY_INIT_PATH" start || true


# ============================================================
# Автозапуск через fs.d (Keenetic хук на монтирование USB)
# ============================================================
echo ""
echo "==> Устанавливаем fs.d хук автозапуска"
mkdir -p /opt/etc/ndm/fs.d/
cat > /opt/etc/ndm/fs.d/100-singbox-manager.sh << 'FS_EOF'
#!/bin/sh
# Keenetic может вызвать несколько раз подряд — lock защищает от race
LOCK=/tmp/singbox-autostart.lock
[ -f "$LOCK" ] && exit 0
touch "$LOCK"

(
    sleep 15
    [ -x /opt/etc/init.d/S99singbox-manager ] && \
        /opt/etc/init.d/S99singbox-manager start > /tmp/singbox-logs/autostart.log 2>&1
    sleep 300
    rm -f "$LOCK"
) &
exit 0
FS_EOF
chmod +x /opt/etc/ndm/fs.d/100-singbox-manager.sh
echo "    OK: fs.d/100-singbox-manager.sh"

# ============================================================
# Cron-watchdog каждые 2 минуты
# ============================================================
echo ""
echo "==> Cron-watchdog (*/2)"
mkdir -p /opt/etc/cron.d
cat > /opt/etc/cron.d/singbox-watchdog << 'CRON_EOF'
*/2 * * * * root /opt/bin/singbox-watchdog.sh
CRON_EOF
chmod 644 /opt/etc/cron.d/singbox-watchdog
echo "    OK: cron.d/singbox-watchdog"

# Запуск crond
if ! ps -w | grep -v grep | grep -q crond; then
    echo "==> Запуск crond"
    crond -c /opt/var/spool/cron/crontabs -l 8
    sleep 1
fi
ps -w | grep -v grep | grep -q crond && echo "    OK: crond запущен" || echo "    FAIL: crond не запущен"

IP=$(ip addr show br0 2>/dev/null | grep "inet " | awk '{print $2}' | cut -d/ -f1 || echo "IP-роутера")
echo ""
echo "==========================================================="
echo "  Sing-box Manager $RELEASE_TAG установлен и запущен!"
echo ""
echo "  Открой: http://$IP:9091"
echo ""
echo "  Логи (в RAM):"
echo "    /tmp/singbox-logs/manager.log"
echo "    /tmp/singbox-logs/sing-box.log"
echo "    /tmp/singbox-logs/hook.log"
echo "    /tmp/singbox-logs/monitor.log"
echo "    /tmp/singbox-logs/watchdog.log"
echo "==========================================================="
