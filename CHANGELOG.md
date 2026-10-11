# Changelog

## [4.0.0] - 2026-10-11

### Added
- **TProxy + REDIRECT** — возвращён рабочий механизм прозрачного проксирования
  - TCP → `nat PREROUTING` → REDIRECT `:1082`
  - UDP → `mangle PREROUTING` → TPROXY `:1081`
- **`internal/iptables/`** — управление iptables-правилами (цепочки `SB_TCP`, `SB_UDP_TRANSIT`)
- **`internal/firewall/`** — удалён (дублировал старый пакет)
- **`internal/singbox/extract.go`** — распаковка `tar.gz` с официального бинарника
- **`WriteNDMSHook()`** — KeeneticOS восстанавливает правила после перестройки netfilter
- **`WriteTProxyInit()`** — init-скрипт `S55singbox-tproxy.sh` для автозапуска

### Changed
- **Sing-box 1.14.2** (официальный, SagerNet) вместо AWG-сборки `1.15.0-alpha.10-awgm.31`
- **`handleApply`** — вместо `ndmc.SetupProxies()` (Proxy10, Proxy11) теперь `iptables.Setup(vlessIPs)`
- **`handleApply`** — резолвит IP VLESS-серверов через `singbox.ResolveServerIPs()` для исключения из перехвата
- **`internal/config/generator.go`** — генерирует `tproxy-in` (1081), `redirect-in` (1082), `mixed-in` (1080) вместо N `mixed-inbound`
- **`scripts/install.sh`** — устанавливает netfilter-хук `50-singbox.sh`, проверяет модули `xt_TPROXY` и `xt_socket`
- **`VERSION`** — `4.0.0`

### Removed
- **TUN-inbound** — не работает на Keenetic (конфликты с NDMS)
- **`ndmc.SetupProxies()`** в `handleApply` — больше не создаёт Proxy10, Proxy11
- **AWG Manager зависимость** — не требует AWG-сборку

### Fixed
- **Трафик не шёл через VPN** — теперь TProxy перехватывает TCP+UDP на уровне iptables
- **«Отправлено растёт, принято — 0»** — исправлено правильной схемой TProxy + REDIRECT

## [3.1.0] - 2026-10-09

### Added
- Официальный Sing-box 1.14.2 вместо AWG-сборки
- DNS-фикс: убран `detour:direct` (падал на 1.14)

## [3.0.1] - 2026-10-09

### Fixed
- `ndmc`: префикс `Proxy` + `BaseIndex=10` (не конфликтует с AWG Manager)

## [3.0.0] - 2026-10-09

### Added
- Keenetic native proxy через `ndmc` (Proxy10, Proxy11, ...)

## [2.0.0] - 2026-10-08

### Added
- TProxy + REDIRECT, стабильная версия
