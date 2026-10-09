package monitor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const ClashAPIURL = "http://127.0.0.1:9090"

// ProxyStatus — статус одного outbound.
type ProxyStatus struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Now     string `json:"now,omitempty"`     // для Selector — текущий выбранный
	All     []string `json:"all,omitempty"`   // для Selector — все варианты
	Delay   int    `json:"delay"`             // последний измеренный delay (0 если неизвестно)
	Alive   bool   `json:"alive"`             // живой ли
}

// FetchAll читает /proxies из Clash API и возвращает карту статусов.
func FetchAll() (map[string]ProxyStatus, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(ClashAPIURL + "/proxies")
	if err != nil {
		return nil, fmt.Errorf("clash api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var raw struct {
		Proxies map[string]struct {
			Type    string   `json:"type"`
			Now     string   `json:"now"`
			All     []string `json:"all"`
			History []struct {
				Delay int `json:"delay"`
			} `json:"history"`
		} `json:"proxies"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	out := make(map[string]ProxyStatus)
	for name, p := range raw.Proxies {
		delay := 0
		if len(p.History) > 0 {
			delay = p.History[len(p.History)-1].Delay
		}
		out[name] = ProxyStatus{
			Name:  name,
			Type:  p.Type,
			Now:   p.Now,
			All:   p.All,
			Delay: delay,
			Alive: delay > 0,
		}
	}
	return out, nil
}
