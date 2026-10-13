#!/bin/sh
# Установщик Sing-box Manager v4.3.3 для Keenetic (Entware).

set -e

REPO="vitosamin/singbox-manager"
RELEASE_TAG="v4.3.3"
MANAGER_BIN_PATH="/opt/bin/singbox-manager"
SINGBOX_BIN_PATH="/opt/etc/sing-box/sing-box"
INIT_PATH="/opt/etc/init.d/S99singbox-manager"
DATA_DIR="/opt/etc/sing-box"
CONFIG_D="${DATA_DIR}/config.d"
TMP_DIR="/tmp"
MARKER="/opt/etc/sing-box/vpn.disabled"

if [ ! -d /opt/etc ]; then
    echo "ОШИБКА: Entware не установлен."
    exit 1
fi

if ! command -v ndmc >/dev/null 2>&1; then
    echo "ПРЕДУПРЕЖДЕНИЕ: ndmc не найден."
fi

KERNEL=$(uname -r)
HAS_TPROXY=0
for p in "/lib/modules/$KERNEL/xt_TPROXY.ko" "/lib/system-modules/xt_TPROXY.ko" \
         "/lib/modules/$KERNEL/xt_socket.ko" "/lib/system-modules/xt_socket.ko"; do
    [ -f "$p" ] && HAS_TPROXY=$((HAS_TPROXY+1))
done

if [ "$HAS_TPROXY" -lt 2 ]; then
    echo ""
    echo "WARN: модули xt_TPROXY / xt_socket не найдены!"
    echo "      Установи компонент 'Модули ядра для Netfilter'."
    echo ""
fi

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

echo "=== Sing-box Manager v4.3.3 Installer ==="
echo "Архитектура: $ARCH -> $MANAGER_BIN (Sing-box: $SINGBOX_ARCH)"

LOADER=""
for candidate in /opt/lib/ld-linux*.so* /opt/lib/ld-*.so*; do
    if [ -f "$candidate" ]; then
        LOADER="$candidate"
        break
    fi
done

if [ -z "$LOADER" ]; then
    LOADER="/opt/lib/ld-linux.so.3"
fi

echo "Загрузчик: $LOADER"

if [ -x "$INIT_PATH" ]; then
    echo "Останавливаем старую версию..."
    "$INIT_PATH" stop 2>/dev/null || true
fi
killall singbox-manager 2>/dev/null || true
killall sing-box 2>/dev/null || true

mkdir -p "$DATA_DIR"
mkdir -p "$CONFIG_D"
mkdir -p /opt/var/run /opt/var/log

echo ""
echo "==> Скачиваем менеджер: $MANAGER_BIN"
cd "$TMP_DIR"
URL="https://github.com/$REPO/releases/download/$RELEASE_TAG/$MANAGER_BIN"

if command -v curl >/dev/null 2>&1; then
    curl -L -o singbox-manager.new "$URL"
elif command -v wget >/dev/null 2>&1; then
    wget -O singbox-manager.new "$URL"
else
    echo "ОШИБКА: нет ни curl, ни wget"
    exit 1
fi

if [ ! -f singbox-manager.new ]; then
    echo "ОШИБКА: не удалось скачать менеджер"
    exit 1
fi

chmod +x singbox-manager.new
mv singbox-manager.new "$MANAGER_BIN_PATH"
chmod +x "$MANAGER_BIN_PATH"
echo "    OK: $MANAGER_BIN_PATH"

if [ ! -f "$SINGBOX_BIN_PATH" ]; then
    echo ""
    echo "==> Скачиваем Sing-box (наш релиз $RELEASE_TAG, $SINGBOX_ARCH)"
    SINGBOX_URL="https://github.com/$REPO/releases/download/$RELEASE_TAG/sing-box-1.14.2-${SINGBOX_ARCH}.tar.gz"

    if command -v curl >/dev/null 2>&1; then
        curl -L -o sing-box.tar.gz "$SINGBOX_URL"
    elif command -v wget >/dev/null 2>&1; then
        wget -O sing-box.tar.gz "$SINGBOX_URL"
    else
        echo "ОШИБКА: нет ни curl, ни wget"
        exit 1
    fi

    if [ ! -f sing-box.tar.gz ]; then
        echo "ОШИБКА: не удалось скачать Sing-box"
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

echo ""
echo "==> Устанавливаем init-скрипт: $INIT_PATH"
cat > "$INIT_PATH" << INIT_EOF
#!/bin/sh
BIN=/opt/bin/singbox-manager
LOGFILE=/opt/var/log/singbox-manager.log
ADDR=":9091"
MARKER=/opt/etc/sing-box/vpn.disabled
SINGBOX=/opt/etc/sing-box/sing-box
LOADER=$LOADER
CONFIG=/opt/etc/sing-box/config.d
TPROXY_INIT=/opt/etc/init.d/S55singbox-tproxy.sh

case "\$1" in
    start)
        if ps -w | grep -v grep | grep -q "\$BIN"; then
            echo "singbox-manager уже запущен"
        else
            echo "Запуск singbox-manager..."
            mkdir -p /opt/var/log
            \$BIN -addr \$ADDR > \$LOGFILE 2>&1 &
            sleep 3
            if ! ps -w | grep -v grep | grep -q "\$BIN"; then
                echo "ОШИБКА. Лог:"
                cat \$LOGFILE
                exit 1
            fi
            echo "OK"
        fi

        if [ -d "\$CONFIG" ] && [ ! -f "\$MARKER" ]; then
            if ! ps -w | grep -v grep | grep -q "sing-box run"; then
                echo "Автозапуск sing-box..."
                \$LOADER \$SINGBOX run -C \$CONFIG > /opt/var/log/sing-box.log 2>&1 &
                sleep 5
            fi
            if [ -x "\$TPROXY_INIT" ] && { ! iptables -t nat -L SB_TCP -n >/dev/null 2>&1 || ! iptables -t mangle -L SB_UDP_TRANSIT -n >/dev/null 2>&1; }; then
                echo "Восстановление iptables-правил..."
                \$TPROXY_INIT start >/dev/null 2>&1
            fi
        fi
        ;;
    stop)
        echo "Остановка..."
        killall singbox-manager 2>/dev/null
        killall sing-box 2>/dev/null
        echo "OK"
        ;;
    restart)
        \$0 stop
        sleep 2
        \$0 start
        ;;
    status)
        if ps -w | grep -v grep | grep -q "\$BIN"; then
            echo "singbox-manager работает"
        else
            echo "singbox-manager не запущен"
        fi
        if ps -w | grep -v grep | grep -q "sing-box run"; then
            echo "sing-box работает"
        else
            echo "sing-box не запущен"
        fi
        ;;
    *)
        echo "Использование: \$0 {start|stop|restart|status}"
        exit 1
        ;;
esac

exit 0
INIT_EOF

chmod +x "$INIT_PATH"

echo ""
echo "==> Устанавливаем cron-бэкап state.json"
CRONDAILY_DIR="/opt/etc/cron.daily"
if [ -d "$CRONDAILY_DIR" ] || mkdir -p "$CRONDAILY_DIR" 2>/dev/null; then
    cat > "$CRONDAILY_DIR/backup-state" << 'CRON_EOF'
#!/bin/sh
BACKUP_DIR="/opt/etc/sing-box/backups"
mkdir -p "$BACKUP_DIR"

DATE=$(date +%Y%m%d)
STATE_SRC="/opt/etc/sing-box/state.json"
CONFIG_SRC="/opt/etc/sing-box/config.d"

[ -f "$STATE_SRC" ] && cp "$STATE_SRC" "$BACKUP_DIR/state-$DATE.json"
[ -d "$CONFIG_SRC" ] && tar czf "$BACKUP_DIR/config-$DATE.tar.gz" -C "$(dirname "$CONFIG_SRC")" "$(basename "$CONFIG_SRC")" 2>/dev/null

find "$BACKUP_DIR" -name "state-*.json" -mtime +7 -delete 2>/dev/null
find "$BACKUP_DIR" -name "config-*.tar.gz" -mtime +7 -delete 2>/dev/null

exit 0
CRON_EOF

    chmod +x "$CRONDAILY_DIR/backup-state"
    echo "    OK: $CRONDAILY_DIR/backup-state"
fi

if [ -x /opt/etc/init.d/S10cron ]; then
    /opt/etc/init.d/S10cron restart >/dev/null 2>&1 || true
fi

echo ""
echo "==> Запускаем менеджер..."
"$INIT_PATH" start

IP=$(ip addr show br0 2>/dev/null | grep "inet " | awk '{print $2}' | cut -d/ -f1 || echo "IP-роутера")
echo ""
echo "==========================================================="
echo "  Sing-box Manager v4.3.3 установлен и запущен!"
echo ""
echo "  Открой в браузере:  http://$IP:9091"
echo ""
echo "  Смени пароль в Настройках после входа!"
echo "==========================================================="
echo ""
echo "Новое в v4.3.3:"
echo "  - Поддержка mipsle-softfloat (старые Keenetic)"
echo "  - Автоопределение архитектуры и загрузчика"
echo "  - Автозапуск sing-box после перезагрузки"
echo "  - Watchdog с grace-period 60 сек при старте"
echo "  - Watchdog НЕ ставит маркер при падении sing-box"
echo "  - Все функции v4.3.2"
