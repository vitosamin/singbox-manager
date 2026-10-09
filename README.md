# Sing-box Manager
# Sing-box Manager

Веб-панель управления **Sing-box** на роутерах **Keenetic** (Entware). Управление VLESS/Trojan/Hysteria2/Shadowsocks-туннелями, раздельная маршрутизация по доменам и CIDR, автоматический failover при падении серверов.

> **Статус:** BETA. Основной функционал работает. Часть фич в работе (см. [CHANGELOG](CHANGELOG.md)).

---

## Что это такое

Один статический бинарник + веб-интерфейс, который:

1. **Устанавливает Sing-box** — скачивает свежую версию с GitHub автоматически.
2. **Раздаёт UI** — открываешь `http://<роутер>:9091` в браузере и управляешь мышкой.
3. **Управляет маршрутизацией** — через списки и правила внутри Sing-box.
4. **Следит за серверами** — переключает селектор на живой сервер при падении.

Работает **прямо на роутере**, не требует внешних сервисов, не отправляет данные наружу.

---

## Возможности

### Управление серверами

* Поддержка **VLESS** (в т.ч. Reality), **Trojan**, **Hysteria2**, **Shadowsocks**.
* Парсинг ссылок `vless://`, `trojan://`, `hysteria2://`, `ss://`.
* Автоматическое имя сервера из `#remark` (флаг, описание).
* Тест задержки в один клик.

### Раздельная маршрутизация

* **Готовые списки** из [itdoginfo/allow-domains](https://github.com/itdoginfo/allow-domains): YouTube, Telegram, Discord, OpenAI, Google AI, Meta, Twitter, TikTok, Netflix и др.
* **Свои правила** — домены и CIDR.
* **Свои `.srs`** — прямые ссылки.
* Каждое правило — **отдельная группа**.

### Приоритеты и failover

* Список серверов в порядке приоритета для каждой группы.
* **Автопереключение** при падении.
* Возврат на первый сервер, когда он снова доступен.
* **Ручные группы** — например, «Reality-only».

### Маршрутизация по IP

* `proxy / direct / block` для конкретного устройства.
* **Приоритет над всеми списками.**

### DNS

* Собственный список DNS (UDP / DoT / DoH / DoQ).
* `dns-router` (AdGuard) и `dns-remote` (через VPN).

### Мониторинг

* Задержка в реальном времени.
* История замеров.
* Статус каждого outbound.

### Журнал

* **В оперативной памяти**, на диск не пишется.
* Размер буфера: 500 / 1000 / 2000 / 5000 строк.

### Безопасность

* **Пароль** (bcrypt + cookie-сессия).
* Смена пароля в один клик.

### Импорт/экспорт

* Всё состояние в `state.json`.

---

## Требования

**Для роутера:**

* Keenetic с Entware (Giga, Ultra, Hopper, Peak и др.).
* Компонент **WireGuard** в KeeneticOS.
* USB-флешка (ext4) с Entware.
* Около 50 МБ свободного места.
* *(Опционально)* **AdGuard Home** — в качестве основного DNS.

**Для сборки:**

* Go 1.23+.
* Linux / macOS для кросс-компиляции.

---

## Установка

### Автоматическая установка (после релиза)

```sh
opkg update
wget -qO- https://github.com/vitosamin/singbox-manager/raw/main/scripts/install.sh | sh
```

После установки открой `http://<IP-роутера>:9091`.

**Пароль по умолчанию:** `admin`. **Смени его сразу** в настройках.

### Ручная установка

```sh
cd /tmp
wget https://github.com/vitosamin/singbox-manager/releases/latest/download/singbox-manager-arm64
chmod +x singbox-manager-arm64
mv singbox-manager-arm64 /opt/bin/singbox-manager
```

Создай init-скрипт:

```sh
cat > /opt/etc/init.d/S99singbox-manager << 'INIT'
#!/bin/sh

case "$1" in
    start)
        /opt/bin/singbox-manager -addr :9091 &
        ;;
    stop)
        killall singbox-manager
        killall sing-box
        ;;
    restart)
        $0 stop
        sleep 1
        $0 start
        ;;
    *)
        echo "Usage: $0 {start|stop|restart}"
        exit 1
        ;;
esac

exit 0
INIT

chmod +x /opt/etc/init.d/S99singbox-manager
/opt/etc/init.d/S99singbox-manager start
```

### Проверка работы

Открой в браузере:

`http://<IP-роутера>:9091`

Проверь статус процесса:

```sh
ps | grep '[s]ingbox-manager'
```

---

## Лицензия

Укажи здесь выбранную лицензию проекта, например MIT, если она соответствует условиям распространения и используемым компонентам.

