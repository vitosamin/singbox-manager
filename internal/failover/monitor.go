package failover

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

const ClashAPIURL = "http://127.0.0.1:9090"
const TestURL = "https://www.google.com/generate_204"

type Group struct {
	SelectorTag  string
	PriorityTags []string
}

type Monitor struct {
	Groups   []Group
	Interval time.Duration

	client *http.Client
	mu     sync.Mutex
}

func NewMonitor(groups []Group) *Monitor {
	return &Monitor{
		Groups:   groups,
		Interval: 10 * time.Second,
		client:   &http.Client{Timeout: 8 * time.Second},
	}
}

func (m *Monitor) Run() {
	m.checkAll()
	ticker := time.NewTicker(m.Interval)
	defer ticker.Stop()
	for {
		<-ticker.C
		m.checkAll()
	}
}

func (m *Monitor) checkAll() {
	for _, g := range m.Groups {
		m.checkGroup(g)
	}
}

func (m *Monitor) checkGroup(g Group) {
	for _, tag := range g.PriorityTags {
		alive, delay, err := m.testProxy(tag)
		if err != nil {
			fmt.Printf("[failover] %s: %s — %v\n", g.SelectorTag, tag, err)
			continue
		}
		if alive {
			current, _ := m.currentSelection(g.SelectorTag)
			if current != tag {
				fmt.Printf("[failover] %s: переключаю на %s (delay=%dms, был %s)\n", g.SelectorTag, tag, delay, current)
				if err := m.selectProxy(g.SelectorTag, tag); err != nil {
					fmt.Printf("[failover] %s: ошибка: %v\n", g.SelectorTag, err)
				}
			} else {
				fmt.Printf("[failover] %s: %s живой (delay=%dms)\n", g.SelectorTag, tag, delay)
			}
			return
		}
		fmt.Printf("[failover] %s: %s мёртв, иду дальше\n", g.SelectorTag, tag)
	}
	fmt.Printf("[failover] %s: все серверы мертвы\n", g.SelectorTag)
}

func (m *Monitor) testProxy(tag string) (bool, int, error) {
	url := fmt.Sprintf("%s/proxies/%s/delay?timeout=5000&url=%s", ClashAPIURL, tag, TestURL)
	resp, err := m.client.Get(url)
	if err != nil {
		return false, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return false, 0, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	var result struct {
		Delay int `json:"delay"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, 0, err
	}
	return result.Delay > 0, result.Delay, nil
}

func (m *Monitor) currentSelection(selectorTag string) (string, error) {
	url := fmt.Sprintf("%s/proxies/%s", ClashAPIURL, selectorTag)
	resp, err := m.client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var result struct {
		Now string `json:"now"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.Now, nil
}

func (m *Monitor) selectProxy(selectorTag, proxyTag string) error {
	url := fmt.Sprintf("%s/proxies/%s", ClashAPIURL, selectorTag)
	body, _ := json.Marshal(map[string]string{"name": proxyTag})
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	return nil
}
