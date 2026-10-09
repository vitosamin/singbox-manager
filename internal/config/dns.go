package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// DNSServer — один DNS-сервер в конфиге Sing-box.
type DNSServer struct {
	Tag     string `json:"tag"`              // внутренний тег (dns-router, dns-remote, ...)
	Type    string `json:"type"`             // udp, tcp, tls, https, quic
	Server  string `json:"server"`           // IP или домен
	Port    int    `json:"port,omitempty"`   // порт (опционально)
	Detour  string `json:"detour,omitempty"` // через какой outbound идти (опционально)
	Enabled bool   `json:"enabled"`          // включён/выключен
	ViaVPN  bool   `json:"via_vpn"`          // идти через VPN-туннель
	Remark  string `json:"remark,omitempty"` // человекочитаемое описание
}

// DNSRule — правило DNS-маршрутизации.
type DNSRule struct {
	RuleSet []string `json:"rule_set,omitempty"` // для каких rule-set
	Server  string   `json:"server"`             // какой DNS использовать
}

// DNSConfig — полная конфигурация DNS.
type DNSConfig struct {
	Servers       []DNSServer `json:"servers"`
	Rules         []DNSRule   `json:"rules"`
	DefaultServer string      `json:"default_server"`
}

// DefaultDNSConfig возвращает конфиг по умолчанию:
//   - основной DNS = системный (роутер с AdGuard, или systemd-resolved на Ubuntu).
//   - резервный = 1.1.1.1 через VPN (выключен по умолчанию).
func DefaultDNSConfig() DNSConfig {
	routerDNS := DetectSystemDNS()
	if routerDNS == "" {
		routerDNS = "8.8.8.8"
	}

	return DNSConfig{
		Servers: []DNSServer{
			{
				Tag:     "dns-router",
				Type:    "udp",
				Server:  routerDNS,
				Enabled: true,
				Remark:  "DNS роутера (AdGuard Home)",
			},
			{
				Tag:     "dns-remote",
				Type:    "tls",
				Server:  "1.1.1.1",
				Port:    853,
				Enabled: false,
				ViaVPN:  true,
				Remark:  "Cloudflare DoT через VPN",
			},
		},
		DefaultServer: "dns-router",
		Rules:         []DNSRule{},
	}
}

// DetectSystemDNS читает первый nameserver из /etc/resolv.conf.
// НЕ пропускает 127.0.0.53/54 — Sing-box может работать через
// systemd-resolved stub, потому что у нас mixed inbound, а не TUN.
func DetectSystemDNS() string {
	f, err := os.Open("/etc/resolv.conf")
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "nameserver") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return fields[1]
			}
		}
	}
	return ""
}

// BuildDNS собирает секцию "dns" для config.json из DNSConfig.
func (c DNSConfig) BuildDNS(proxyDetour string) map[string]interface{} {
	servers := []map[string]interface{}{}
	rules := []map[string]interface{}{}

	for _, s := range c.Servers {
		if !s.Enabled {
			continue
		}

		server := map[string]interface{}{
			"type":   s.Type,
			"tag":    s.Tag,
			"server": s.Server,
		}
		if s.Port > 0 {
			server["server_port"] = s.Port
		}
		if s.ViaVPN && proxyDetour != "" {
			server["detour"] = proxyDetour
		}
		servers = append(servers, server)
	}

	for _, r := range c.Rules {
		if len(r.RuleSet) == 0 {
			continue
		}
		rules = append(rules, map[string]interface{}{
			"rule_set": r.RuleSet,
			"server":   r.Server,
		})
	}

	if c.DefaultServer != "" {
		rules = append(rules, map[string]interface{}{
			"server": c.DefaultServer,
		})
	}

	result := map[string]interface{}{
		"servers": servers,
	}
	if len(rules) > 0 {
		result["rules"] = rules
	}
	return result
}

// Validate проверяет конфиг.
func (c DNSConfig) Validate() error {
	if len(c.Servers) == 0 {
		return fmt.Errorf("нет ни одного DNS-сервера")
	}
	if c.DefaultServer == "" {
		return fmt.Errorf("не задан default_server")
	}
	found := false
	for _, s := range c.Servers {
		if s.Tag == c.DefaultServer && s.Enabled {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("default_server %q не найден или выключен", c.DefaultServer)
	}
	return nil
}
