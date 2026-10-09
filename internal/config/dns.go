package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// DNSServer — один DNS-сервер в конфиге Sing-box.
type DNSServer struct {
	Tag     string `json:"tag"`
	Type    string `json:"type"`             // udp, tcp, tls, https, quic
	Server  string `json:"server"`
	Port    int    `json:"port,omitempty"`
	Detour  string `json:"detour,omitempty"` // через какой outbound (например "direct")
	Enabled bool   `json:"enabled"`
	ViaVPN  bool   `json:"via_vpn"`
	Remark  string `json:"remark,omitempty"`
}

// DNSRule — правило DNS-маршрутизации.
type DNSRule struct {
	RuleSet []string `json:"rule_set,omitempty"`
	Server  string   `json:"server"`
}

// DNSConfig — полная конфигурация DNS.
type DNSConfig struct {
	Servers       []DNSServer `json:"servers"`
	Rules         []DNSRule   `json:"rules"`
	DefaultServer string      `json:"default_server"`
}

// DefaultDNSConfig возвращает конфиг по умолчанию.
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

// BuildDNS собирает секцию "dns" для config.json.
// proxyDetour — имя VPN-селектора (например "proxy-default").
//
// КЛЮЧЕВОЕ: для серверов БЕЗ via_vpn добавляем "detour": "direct",
// чтобы DNS-запросы шли напрямую через ppp0, а не через TUN (иначе петля).
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

		// DNS через VPN — только если via_vpn=true и есть proxyDetour
		if s.ViaVPN && proxyDetour != "" {
			server["detour"] = proxyDetour
		} else {
			// ВСЁ ОСТАЛЬНОЕ — напрямую (не через TUN)
			server["detour"] = "direct"
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
