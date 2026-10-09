#!/bin/sh
# Установщик Sing-box Manager для Keenetic (Entware).
# Запуск:
#   wget -qO- https://github.com/vitosamin/singbox-manager/raw/main/scripts/install.sh | sh
# Или:
#   curl -sL https://github.com/vitosamin/singbox-manager/raw/main/scripts/install.sh | sh

set -e

REPO="vitosamin/singbox-manager"
BIN_PATH="/opt/bin/singbox-manager"
INIT_PATH="/opt/etc/init.d/S99singbox-manager"
DATA_DIR="/opt/etc/sing-box"
TMP_DIR="/tmp"

# --- Проверки ---
if [ ! -d /opt/etc ]; then
    echo "ОШИБКА: Entware не установлен. Установи поддержку OPKG в KeeneticOS."
    exit 1
fi

# --- Определение архитектуры ---
ARCH=$(uname -m)
case "$ARCH" in
    aarch64|arm64) BIN_NAME="singbox-manager-arm64" ;;
    mips)          BIN_NAME="singbox-manager-mipsle" ;;
    *)
        echo "ОШИБКА: неподдерживаемая архитектура: $ARCH"
        exit 1
        ;;
esac

echo "=== Sing-box Manager Installer ==="
echo "Архитектура: $ARCH -> $BIN_NAME"

# --- Скачивание бинарника ---
URL="https://github.com/$REPO/releases/latest/download/$BIN_NAME"
echo "Скачиваем $URL ..."

cd "$TMP_DIR"
if command -v curl >/dev/null 2>&1; then
    curl -L -o singbox-manager.new "$URL"
elif command -v wget >/dev/null 2>&1; then
    wget -O singbox-manager.new "$URL"
else
    echo "ОШИБКА: нет ни curl, ни wget"
    exit 1
fi

if [ ! -f singbox-manager.new ]; then
    echo "ОШИБКА: не удалось скачать бинарник"
    exit 1
fi

chmod +x singbox-manager.new

# --- Остановка старой версии ---
if [ -x "$INIT_PATH" ]; then
    echo "Останавливаем старую версию..."
    "$INIT_PATH" stop 2>/dev/null || true
fi
killall singbox-manager 2>/dev/null || true

# --- Установка бинарника ---
echo "Устанавливаем в $BIN_PATH ..."
mv singbox-manager.new "$BIN_PATH"
chmod +x "$BIN_PATH"

# --- Каталог данных ---
mkdir -p "$DATA_DIR"

# --- Init-скрипт ---
echo "Устанавливаем init-скрипт в $INIT_PATH ..."
cat > "$INIT_PATH" << 'INIT_EOF'
#!/bin/sh
BIN=/opt/bin/singbox-manager
PIDFILE=/opt/var/run/singbox-manager.pid
LOGFILE=/opt/var/log/singbox-manager.log
ADDR=":9091"

is_running() {
    if [ -f "$PIDFILE" ]; then
        PID=$(cat "$PIDFILE")
        if [ -n "$PID" ] && kill -0 "$PID" 2>/dev/null; then
            return 0
        fi
    fi
    return 1
}

start() {
    if is_running; then
        echo "singbox-manager уже запущен (PID $(cat $PIDFILE))"
        return 0
    fi
    echo "Запуск singbox-manager..."
    mkdir -p /opt/var/run /opt/var/log
    nohup $BIN -addr $ADDR > $LOGFILE 2>&1 &
    echo $! > $PIDFILE
    sleep 1
    if is_running; then
        echo "OK, PID $(cat $PIDFILE)"
        return 0
    fi
    echo "ОШИБКА: не удалось запустить"
    return 1
}

stop() {
    if ! is_running; then
        echo "singbox-manager не запущен"
        return 0
    fi
    PID=$(cat $PIDFILE)
    echo "Остановка singbox-manager (PID $PID)..."
    kill "$PID" 2>/dev/null
    sleep 1
    if is_running; then
        kill -9 "$PID" 2>/dev/null
    fi
    rm -f "$PIDFILE"
    killall sing-box 2>/dev/null
    echo "OK"
}

restart() {
    stop
    sleep 1
    start
}

status() {
    if is_running; then
        echo "singbox-manager работает (PID $(cat $PIDFILE))"
        return 0
    fi
    echo "singbox-manager не запущен"
    return 1
}

case "$1" in
    start)   start ;;
    stop)    stop ;;
    restart) restart ;;
    status)  status ;;
    *)
        echo "Использование: $0 {start|stop|restart|status}"
        exit 1
        ;;
esac

exit 0
INIT_EOF

chmod +x "$INIT_PATH"

# --- Запуск ---
echo "Запускаем..."
"$INIT_PATH" start

# --- Итог ---
IP=$(ip addr show br0 2>/dev/null | grep "inet " | awk '{print $2}' | cut -d/ -f1 || echo "IP-роутера")
echo ""
echo "═══════════════════════════════════════════════════════════"
echo "  Sing-box Manager установлен и запущен!"
echo ""
echo "  Открой в браузере:  http://$IP:9091"
echo "  Пароль по умолчанию: admin"
echo ""
echo "  Смени пароль сразу после входа!"
echo "═══════════════════════════════════════════════════════════"
echo ""
echo "Управление:"
echo "  $INIT_PATH start    — запустить"
echo "  $INIT_PATH stop     — остановить"
echo "  $INIT_PATH restart  — перезапустить"
echo "  $INIT_PATH status   — статус"
