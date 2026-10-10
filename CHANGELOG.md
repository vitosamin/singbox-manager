# Changelog

## [3.0.0] — 2026-10-10

**MAJOR:** переход на Keenetic native proxy через `ndmc`. Убран iptables.

### Изменено
- **Убран iptables** (TProxy + REDIRECT) — заменён на `ndmc` (Keenetic NDMS CLI).
- **Убран NDMS-хук** `50-singbox.sh` — больше не нужен.
- **Убран `S55singbox-tproxy.sh`** — больше не нужен.
- **`config.json` → `config.d/`** — фрагментированный конфиг Sing-box.
- **`iptables/manager.go` → `ndmc/manager.go`** — новый модуль.
- **Запуск Sing-box:** `-c config.json` → `-C config.d`.
- **Множество серверов:** каждый сервер = свой mixed-inbound (1080, 1081, ...).
- **Bundled Sing-box:** AWG-сборка `1.15.0-alpha.10-awgm.31` с поддержкой `flow: xtls-rprx-vision`.

### Добавлено
- **`ndmc.SetupProxies()`** — создание прокси в Keenetic.
- **`ndmc.CleanupAllProxies()`** — удаление прокси.
- **6+ Reality-серверов одновременно** (устранён баг с 2 Reality).
- **Совместимость с HR Neo** и Keenetic policy routing.
- **Скачивание Sing-box** из нашего GitHub Release.

### Удалено
- `internal/iptables/` — целиком.
- iptables TProxy + REDIRECT.
- NDMS-хук.
- `S55singbox-tproxy.sh`.
- `ResolveServerIPs()` (больше не нужна).

### Известные ограничения
- Требуется `ndmc` (есть в KeeneticOS из коробки).
- Требуется ручная настройка HR Neo / Keenetic policy для маршрутизации.

## [2.0.0] — 2026-10-10

Стабильная версия. TProxy + REDIRECT (iptables).

### Изменено
- TUN заменён на TProxy + REDIRECT — работает на Keenetic.
- TCP через REDIRECT (порт 1082), UDP через TPROXY (порт 1081).
- Правильный NDMS-хук с проверкой `$table` и защитой от гонки.
- Циклическое удаление `ip rule` (исправлены дубликаты).
- Убран `flow: xtls-rprx-vision` — не работает на кастомной сборке.
- WriteInitScripts() вызывается ДО WriteNDMSHook().

## [0.1.0] — 2026-10-09

Первая публичная BETA.

### Добавлено
- Веб-интерфейс на Go + JS.
- VLESS, Trojan, Hysteria2, Shadowsocks.
- Раздельная маршрутизация (домены + CIDR).
- Готовые списки itdoginfo.
- Свои правила и свои .srs.
- Приоритеты и failover.
- Ручные группы.
- Маршрутизация по IP.
- DNS-серверы.
- Мониторинг задержек.
- Журнал в оперативке.
- Пароль на UI.
- Экспорт/импорт state.json.
