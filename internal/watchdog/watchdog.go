// Package watchdog следит за состоянием sing-box и iptables.
//
// Логика:
//   - Каждые 15 секунд проверяет Clash API http://127.0.0.1:9090/version.
//   - Если 3 провала подряд — снимает iptables-правила (интернет идёт напрямую)
//     и ставит маркер vpn.disabled.
//   - Если sing-box поднялся — НЕ восстанавливает правила автоматически
//     (пользователь сам нажмёт Start или Apply).
//   - Grace-period 10 секунд после Apply — не проверяет, чтобы не снять
//     правила во время перезапуска sing-box.
//
// Watchdog работает в фоне, не блокирует основной поток.
package watchdog

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/shapovalenko/keenetic-singbox-manager/internal/iptables"
	"github.com/shapovalenko/keenetic-singbox-manager/internal/vpn"
)

const (
	// CheckInterval — как часто проверять sing-box.
	CheckInterval = 15 * time.Second

	// GracePeriod — сколько ждать после Apply, не проверяя sing-box.
	GracePeriod = 10 * time.Second

	// MaxFailures — сколько провалов подряд до снятия правил.
	MaxFailures = 3

	// ClashAPIURL — URL Clash API для проверки.
	ClashAPIURL = "http://127.0.0.1:9090/version"
)

var (
	// applyInProgress — флаг, что сейчас идёт Apply. Watchdog пропускает проверки.
	applyInProgress atomic.Bool

	// lastApplyTime — время последнего Apply. Для grace-period.
	lastApplyTime atomic.Int64

	// running — флаг, что watchdog уже запущен.
	running atomic.Bool
)

// SetApplyInProgress — устанавливает флаг «идёт Apply».
// Вызывается из handleApply перед началом и после завершения.
func SetApplyInProgress(inProgress bool) {
	if inProgress {
		applyInProgress.Store(true)
		lastApplyTime.Store(time.Now().Unix())
	} else {
		applyInProgress.Store(false)
		lastApplyTime.Store(time.Now().Unix())
	}
}

// isApplyGrace — true, если мы в grace-period после Apply.
func isApplyGrace() bool {
	if applyInProgress.Load() {
		return true
	}
	last := lastApplyTime.Load()
	if last == 0 {
		return false
	}
	return time.Since(time.Unix(last, 0)) < GracePeriod
}

// Start — запускает watchdog в фоне (горутина).
// Идемпотентно: если уже запущен — ничего не делает.
func Start() {
	if !running.CompareAndSwap(false, true) {
		fmt.Println("[watchdog] уже запущен")
		return
	}

	fmt.Printf("[watchdog] запущен (интервал %v, grace %v, макс. провалов %d)\n",
		CheckInterval, GracePeriod, MaxFailures)

	go func() {
		failures := 0
		ticker := time.NewTicker(CheckInterval)
		defer ticker.Stop()

		for range ticker.C {
			// Пропускаем проверку в grace-period после Apply.
			if isApplyGrace() {
				failures = 0
				continue
			}

			// Если VPN выключен пользователем — не проверяем.
			if vpn.IsDisabled() {
				failures = 0
				continue
			}

			if checkSingBox() {
				failures = 0
				continue
			}

			failures++
			fmt.Printf("[watchdog] sing-box не отвечает (%d/%d)\n", failures, MaxFailures)

			if failures >= MaxFailures {
				handleFailure()
				failures = 0
			}
		}
	}()
}

// checkSingBox — true, если Clash API отвечает.
func checkSingBox() bool {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(ClashAPIURL)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// handleFailure — снимает iptables-правила (интернет идёт напрямую).
// Ставит маркер vpn.disabled, чтобы NDMS-хук не восстановил правила.
func handleFailure() {
	fmt.Println("[watchdog] sing-box упал — снимаем iptables-правила")

	// Ставим маркер, чтобы NDMS-хук и cron не восстановили.
	_ = vpn.ForceDisableMarker()

	// Снимаем правила.
	if err := iptables.Cleanup(); err != nil {
		fmt.Printf("[watchdog] ошибка снятия правил: %v\n", err)
		return
	}

	fmt.Println("[watchdog] правила сняты, интернет идёт напрямую")
}
