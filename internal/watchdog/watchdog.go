// Package watchdog следит за состоянием sing-box и iptables.
//
// Логика:
//   - StartupGrace секунд после старта — не проверяем (даём sing-box подняться).
//   - GracePeriod секунд после Apply — не проверяем (идёт перезапуск).
//   - Каждые CheckInterval секунд проверяем Clash API.
//   - MaxFailures провалов подряд → снимаем iptables-правила.
//   - НЕ ставим маркер vpn.disabled — это делает только пользователь через Stop.
//   - Через onFailure callback сообщаем наверх (обновить vpn_enabled в state).
package watchdog

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/shapovalenko/keenetic-singbox-manager/internal/iptables"
)

const (
	CheckInterval = 15 * time.Second
	GracePeriod   = 10 * time.Second
	StartupGrace  = 60 * time.Second
	MaxFailures   = 3
	ClashAPIURL   = "http://127.0.0.1:9090/version"
)

var (
	applyInProgress atomic.Bool
	lastApplyTime   atomic.Int64
	running         atomic.Bool
	startTime       time.Time
	onFailure       func()
)

// SetOnFailure — callback, который вызовется при падении sing-box.
func SetOnFailure(fn func()) {
	onFailure = fn
}

// SetApplyInProgress — устанавливает флаг «идёт Apply».
func SetApplyInProgress(inProgress bool) {
	applyInProgress.Store(inProgress)
	lastApplyTime.Store(time.Now().Unix())
}

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

func isStartupGrace() bool {
	return time.Since(startTime) < StartupGrace
}

// Start — запускает watchdog в фоне.
func Start() {
	if !running.CompareAndSwap(false, true) {
		fmt.Println("[watchdog] уже запущен")
		return
	}

	startTime = time.Now()

	fmt.Printf("[watchdog] запущен (интервал %v, grace %v, startup-grace %v, макс. провалов %d)\n",
		CheckInterval, GracePeriod, StartupGrace, MaxFailures)

	go func() {
		failures := 0
		ticker := time.NewTicker(CheckInterval)
		defer ticker.Stop()

		for range ticker.C {
			if isStartupGrace() {
				failures = 0
				continue
			}
			if isApplyGrace() {
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

func checkSingBox() bool {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(ClashAPIURL)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// handleFailure — снимает iptables-правила.
// НЕ ставит маркер vpn.disabled — это делает только Stop из UI.
func handleFailure() {
	fmt.Println("[watchdog] sing-box упал — снимаем iptables-правила")

	if err := iptables.Cleanup(); err != nil {
		fmt.Printf("[watchdog] ошибка снятия правил: %v\n", err)
	}

	if onFailure != nil {
		onFailure()
	}

	fmt.Println("[watchdog] правила сняты, интернет идёт напрямую")
}
