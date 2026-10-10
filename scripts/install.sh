#!/bin/sh
# Установщик Sing-box Manager v3.0.0 для Keenetic (Entware).
# Запуск:
#   wget -qO- https://github.com/vitosamin/singbox-manager/raw/main/scripts/install.sh | sh

set -e

REPO="vitosamin/singbox-manager"
MANAGER_BIN_PATH="/opt/bin/singbox-manager"
SINGBOX_BIN_PATH="/opt/etc/sing-box/sing-box"
INIT_PATH="/opt/etc/init.d/S99singbox-manager"
DATA_DIR="/opt/etc/sing-box"
CONFIG_D="${DATA_DIR}/config.d"
TMP_DIR="/tmp"

# --- Проверки ---
if [ ! -d /opt/etc ]; then
    echo "ОШИБКА: Entware не установлен. Установи поддержку OPKG в KeeneticOS."
    exit 1
fi

if ! command -v ndmc >/dev/null 2>&1; then
    echo "ПРЕДУПРЕЖДЕНИЕ: ndmc не найден. Прокси в Keenetic не будут созданы автоматически."
    echo "             Установка продолжится, но маршрутизация не заработает."
fi

# --- Определение архитектуры ---
ARCH=$(uname -m)
case "$ARCH" in
    aarch64|arm64) MANAGER_BIN="singbox-manager-arm64" ;;
    mips)          MANAGER_BIN="singbox-manager-mipsle" ;;
    *)
        echo "ОШИБКА: неподдерживаемая архитектура: $ARCH"
        exit 1
        ;;
esac

echo "=== Sing-box Manager v3.0.0 Installer ==="
echo "Архитектура: $ARCH -> $MANAGER_BIN"

# --- Остановка старой версии ---
if [ -x "$INIT_PATH" ]; then
    echo "Останавливаем старую версию..."
    "$INIT_PATH" stop 2>/dev/null || true
fi
killall singbox-manager 2>/dev/null || true

# --- Убираем старые iptables-скрипты (если остались от v2) ---
rm -f /opt/etc/init.d/S55singbox-tproxy.sh
rm -f /opt/etc/ndm/netfilter.d/50-singbox.sh

# --- Каталоги ---
mkdir -p "$DATA_DIR"
mkdir -p "$CONFIG_D"
mkdir -p /opt/var/run /opt/var/log

# --- Скачивание менеджера ---
echo ""
echo "==> Скачиваем менеджер: $MANAGER_BIN"
cd "$TMP_DIR"
URL="https://github.com/$REPO/releases/latest/download/$MANAGER_BIN"

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

# --- Скачивание бинарника Sing-box (официальный 1.14.2) ---
if [ ! -f "$SINGBOX_BIN_PATH" ]; then
    echo ""
    echo "==> Скачиваем Sing-box 1.14.2 (официальный, SagerNet)"
    SINGBOX_URL="https://github.com/SagerNet/sing-box/releases/download/v1.14.2/sing-box-1.14.2-linux-arm64.tar.gz"

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

    # Распаковываем
    tar xzf sing-box.tar.gz
    cp sing-box-1.14.2-linux-arm64/sing-box "$SINGBOX_BIN_PATH"
    cp sing-box-1.14.2-linux-arm64/libcronet.so "$DATA_DIR/" 2>/dev/null || true
    chmod +x "$SINGBOX_BIN_PATH"

    # Чистим
    rm -rf sing-box.tar.gz sing-box-1.14.2-linux-arm64

    echo "    OK: $SINGBOX_BIN_PATH"
else
    echo ""
    echo "==> Sing-box уже установлен: $SINGBOX_BIN_PATH"
fi

# --- Init-скрипт менеджера ---
echo ""
echo "==> Устанавливаем init-скрипт: $INIT_PATH"
cat > "$INIT_PATH" << 'INIT_EOF'
#!/bin/sh
# Init-скрипт для Sing-box Manager (Keenetic, Entware).
BIN=/opt/bin/singbox-manager
LOGFILE=/opt/var/log/singbox-manager.log
ADDR=":9091"

case "$1" in
    start)
        if ps -w | grep -v grep | grep -q "$BIN"; then
            echo "singbox-manager уже запущен"
            exit 0
        fi
        echo "Запуск singbox-manager..."
        mkdir -p /opt/var/log
        $BIN -addr $ADDR > $LOGFILE 2>&1 &
        sleep 3
        if ps -w | grep -v grep | grep -q "$BIN"; then
            echo "OK"
        else
            echo "ОШИБКА. Лог:"
            cat $LOGFILE
            exit 1
        fi
        ;;
    stop)
        echo "Остановка..."
        killall singbox-manager 2>/dev/null
        killall sing-box 2>/dev/null
        rm -f /opt/var/run/singbox-manager.pid
        echo "OK"
        ;;
    restart)
        $0 stop
        sleep 2
        $0 start
        ;;
    status)
        if ps -w | grep -v grep | grep -q "$BIN"; then
            echo "singbox-manager работает"
        else
            echo "singbox-manager не запущен"
        fi
        ;;
    *)
        echo "Использование: $0 {start|stop|restart|status}"
        exit 1
        ;;
esac

exit 0
INIT_EOF

chmod +x "$INIT_PATH"

# --- Запуск ---
echo ""
echo "==> Запускаем менеджер..."
"$INIT_PATH" start

# --- Итог ---
IP=$(ip addr show br0 2>/dev/null | grep "inet " | awk '{print $2}' | cut -d/ -f1 || echo "IP-роутера")
echo ""
echo "═══════════════════════════════════════════════════════════"
echo "  Sing-box Manager v3.0.0 установлен и запущен!"
echo ""
echo "  Открой в браузере:  http://$IP:9091"
echo "  Пароль по умолчанию: admin"
echo ""
echo "  Смени пароль сразу после входа!"
echo "═══════════════════════════════════════════════════════════"
echo ""
echo "Как пользоваться:"
echo "  1. Открой UI → добавь серверы (VLESS-ссылки)"
echo "  2. Включи списки обхода (YouTube, Telegram, ...)"
echo "  3. Нажми «Применить»"
echo ""
echo "  При «Применить» менеджер:"
echo "    - сгенерирует config.d/ для Sing-box"
echo "    - создаст прокси в Keenetic через ndmc"
echo "    - запустит Sing-box"
echo ""
echo "  Далее настрой маршрутизацию в HR Neo / Keenetic policy."
echo ""
echo "Управление:"
echo "  $INIT_PATH start    — запустить менеджер"
echo "  $INIT_PATH stop     — остановить"
echo "  $INIT_PATH restart  — перезапустить"
echo "  $INIT_PATH status   — статус"
