package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/shapovalenko/keenetic-singbox-manager/internal/ruleset"
	"github.com/shapovalenko/keenetic-singbox-manager/internal/subscription"
)

// BasePort — базовый порт первого mixed-inbound. Каждый сервер = 1080, 1081, ...
const BasePort = 1080

// TProxyPort — порт TProxy-inbound (UDP+TCP).
const TProxyPort = 1081

// RedirectPort — порт REDIRECT-inbound (TCP, запасной).
const RedirectPort = 1082

// ConfigDir — каталог с фрагментами конфига для `sing-box -C`.
const ConfigDirName = "config.d"

// ConfigDir возвращает абсолютный путь к каталогу config.d.
func ConfigDir(installDir string) string {
	return filepath.Join(installDir, ConfigDirName)
}

// GenerateV3 — генерирует config.d/ с фрагментами.
// Inbounds: tproxy-in (1081), redirect-in (1082), mixed-in (1080).
// Outbounds: N серверов + M селекторов (по группам).
// Route rules: все inbound → селектор "proxy-default".
func GenerateV3(
	enabledIDs []string,
	customRules []ruleset.CustomRule,
	customSRS []ruleset.CustomSRS,
	proxies []subscription.Proxy,
	groups GroupConfig,
	dnsCfg DNSConfig,
	devices []DeviceRule,
	installDir string,
) error {
	if len(proxies) == 0 {
		return fmt.Errorf("нет серверов для генерации конфига")
	}

	cfgDir := ConfigDir(installDir)
	if err := os.RemoveAll(cfgDir); err != nil {
		return fmt.Errorf("remove config.d: %w", err)
	}
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		return fmt.Errorf("mkdir config.d: %w", err)
	}

	if err := writeJSONFile(filepath.Join(cfgDir, "00-base.json"), buildBase()); err != nil {
		return fmt.Errorf("00-base: %w", err)
	}

	tunnels, err := buildTunnels(proxies, groups)
	if err != nil {
		return fmt.Errorf("10-tunnels: %w", err)
	}
	if err := writeJSONFile(filepath.Join(cfgDir, "10-tunnels.json"), tunnels); err != nil {
		return fmt.Errorf("10-tunnels: %w", err)
	}

	if err := writeJSONFile(filepath.Join(cfgDir, "17-dns-rewrites.json"), buildDNSRewrites()); err != nil {
		return fmt.Errorf("17-dns-rewrites: %w", err)
	}

	if err := writeJSONFile(filepath.Join(cfgDir, "99-defaults.json"), buildDefaults(dnsCfg)); err != nil {
		return fmt.Errorf("99-defaults: %w", err)
	}

	fmt.Printf("[config] записан %s\n", cfgDir)
	fmt.Printf("  серверов: %d, tproxy=%d, redirect=%d, mixed=%d\n",
		len(proxies), TProxyPort, RedirectPort, BasePort)
	return nil
}

// buildBase — базовые настройки.
func buildBase() map[string]interface{} {
	return map[string]interface{}{
		"log": map[string]interface{}{
			"level":     "info",
			"timestamp": true,
		},
		"experimental": map[string]interface{}{
			"clash_api": map[string]interface{}{
				"external_controller": "127.0.0.1:9090",
			},
			"cache_file": map[string]interface{}{
				"enabled": false,
			},
		},
		"outbounds": []map[string]interface{}{
			{"type": "direct", "tag": "direct"},
			{"type": "block", "tag": "block"},
		},
	}
}

// buildTunnels — TProxy + REDIRECT + mixed inbounds + N outbounds + M селекторов + route rules.
//
// Селекторы: для каждой группы (в которой есть enabled-серверы) создаётся
// selector с тегом "proxy-<group>". Default = первый enabled-тег.
//
// Route rule: все inbounds → "proxy-default" (если группа "default" существует)
// или первый селектор. Failover может переключать селектор между серверами.
func buildTunnels(proxies []subscription.Proxy, groups GroupConfig) (map[string]interface{}, error) {
	inbounds := []map[string]interface{}{
		{
			"type":        "tproxy",
			"tag":         "tproxy-in",
			"listen":      "0.0.0.0",
			"listen_port": TProxyPort,
			"network":     []string{"tcp", "udp"},
		},
		{
			"type":        "redirect",
			"tag":         "redirect-in",
			"listen":      "0.0.0.0",
			"listen_port": RedirectPort,
		},
		{
			"type":        "mixed",
			"tag":         "mixed-in",
			"listen":      "127.0.0.1",
			"listen_port": BasePort,
		},
	}

	// Outbounds — серверы.
	outbounds := make([]map[string]interface{}, 0, len(proxies))
	allTags := make([]string, 0, len(proxies))
	for _, p := range proxies {
		ob := proxyToOutbound(p)
		if ob == nil {
			return nil, fmt.Errorf("неподдерживаемый протокол: %s", p.Protocol)
		}
		ob["tag"] = p.Tag
		outbounds = append(outbounds, ob)
		allTags = append(allTags, p.Tag)
	}

	// Селекторы — по группам.
	selectorTags := make([]string, 0, len(groups))
	for name, g := range groups {
		tags := g.PriorityTags()
		if len(tags) == 0 {
			continue
		}
		selectorTag := "proxy-" + name
		selector := map[string]interface{}{
			"type":      "selector",
			"tag":       selectorTag,
			"outbounds": tags,
			"default":   tags[0],
		}
		outbounds = append(outbounds, selector)
		selectorTags = append(selectorTags, selectorTag)
		fmt.Printf("[config] selector %s → %v\n", selectorTag, tags)
	}

	// Route rules: все inbound → proxy-default (если есть), иначе первый селектор, иначе первый сервер.
	defaultOutbound := "direct"
	if hasTag(selectorTags, "proxy-default") {
		defaultOutbound = "proxy-default"
	} else if len(selectorTags) > 0 {
		defaultOutbound = selectorTags[0]
	} else if len(allTags) > 0 {
		defaultOutbound = allTags[0]
	}

	rules := []map[string]interface{}{
		{
			"inbound":  []string{"tproxy-in", "redirect-in", "mixed-in"},
			"outbound": defaultOutbound,
		},
	}

	fmt.Printf("[config] route: all inbounds → %s\n", defaultOutbound)

	return map[string]interface{}{
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"route": map[string]interface{}{
			"rules": rules,
			"final": "direct",
		},
	}, nil
}

// hasTag — проверяет наличие строки в слайсе.
func hasTag(tags []string, target string) bool {
	for _, t := range tags {
		if t == target {
			return true
		}
	}
	return false
}

// buildDNSRewrites — DNS-перехват для Keenetic-доменов.
func buildDNSRewrites() map[string]interface{} {
	return map[string]interface{}{
		"dns": map[string]interface{}{
			"servers": []map[string]interface{}{
				{
					"type":   "udp",
					"tag":    "keendns-router",
					"server": "127.0.0.1",
				},
			},
			"rules": []map[string]interface{}{
				{
					"domain": []string{"my.keenetic.net", "my.netcraze.net"},
					"domain_suffix": []string{
						"keenetic.pro", "keenetic.link", "keenetic.name",
						"keenetic.io", "netcraze.pro", "netcraze.net",
						"netcraze.io", "crazedns.ru",
					},
					"server": "keendns-router",
				},
			},
		},
	}
}

// buildDefaults — стратегия, resolver.
func buildDefaults(dnsCfg DNSConfig) map[string]interface{} {
	_ = dnsCfg
	return map[string]interface{}{
		"dns": map[string]interface{}{
			"servers": []map[string]interface{}{
				{
					"type":   "udp",
					"tag":    "dns-bootstrap",
					"server": "1.1.1.1",
				},
			},
			"optimistic": true,
			"strategy":   "prefer_ipv4",
		},
		"route": map[string]interface{}{
			"default_domain_resolver": map[string]interface{}{
				"server": "dns-bootstrap",
			},
		},
	}
}

// writeJSONFile — записывает JSON-файл с отступами.
func writeJSONFile(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// Generate — старый API, оставлен для совместимости.
func Generate(
	enabledIDs []string,
	customRules []ruleset.CustomRule,
	customSRS []ruleset.CustomSRS,
	proxies []subscription.Proxy,
	groups GroupConfig,
	dnsCfg DNSConfig,
	devices []DeviceRule,
	outPath string,
) error {
	installDir := filepath.Dir(outPath)
	return GenerateV3(
		enabledIDs, customRules, customSRS,
		proxies, groups, dnsCfg, devices,
		installDir,
	)
}

// -------------------- outbound-хелперы --------------------

func proxyToOutbound(p subscription.Proxy) map[string]interface{} {
	switch p.Protocol {
	case "vless":
		return vlessOutbound(p)
	case "trojan":
		return trojanOutbound(p)
	case "hysteria2":
		return hysteria2Outbound(p)
	case "shadowsocks":
		return shadowsocksOutbound(p)
	default:
		return nil
	}
}

func vlessOutbound(p subscription.Proxy) map[string]interface{} {
	ob := map[string]interface{}{
		"type": "vless", "tag": p.Tag, "server": p.Server,
		"server_port": p.Port, "uuid": p.UUID,
	}
	if p.Security == "tls" || p.Security == "reality" {
		tls := map[string]interface{}{"enabled": true}
		if p.SNI != "" {
			tls["server_name"] = p.SNI
		}
		if p.Fingerprint != "" {
			tls["utls"] = map[string]interface{}{"enabled": true, "fingerprint": p.Fingerprint}
		}
		if len(p.ALPN) > 0 {
			tls["alpn"] = p.ALPN
		}
		if p.Security == "reality" {
			reality := map[string]interface{}{"enabled": true}
			if p.PublicKey != "" {
				reality["public_key"] = p.PublicKey
			}
			if p.ShortID != "" {
				reality["short_id"] = p.ShortID
			}
			tls["reality"] = reality
		}
		ob["tls"] = tls
	}
	if p.Flow != "" {
		ob["flow"] = p.Flow
	}
	if t := buildTransport(p); t != nil {
		ob["transport"] = t
	}
	return ob
}

func trojanOutbound(p subscription.Proxy) map[string]interface{} {
	ob := map[string]interface{}{
		"type": "trojan", "tag": p.Tag, "server": p.Server,
		"server_port": p.Port, "password": p.Password,
	}
	if p.SNI != "" {
		ob["tls"] = map[string]interface{}{"enabled": true, "server_name": p.SNI}
	}
	if t := buildTransport(p); t != nil {
		ob["transport"] = t
	}
	return ob
}

func hysteria2Outbound(p subscription.Proxy) map[string]interface{} {
	ob := map[string]interface{}{
		"type": "hysteria2", "tag": p.Tag, "server": p.Server,
		"server_port": p.Port, "password": p.Password,
	}
	tls := map[string]interface{}{"enabled": true}
	if p.SNI != "" {
		tls["server_name"] = p.SNI
	}
	ob["tls"] = tls
	if p.Obfs != "" {
		ob["obfs"] = map[string]interface{}{"type": p.Obfs, "password": p.ObfsPassword}
	}
	return ob
}

func shadowsocksOutbound(p subscription.Proxy) map[string]interface{} {
	return map[string]interface{}{
		"type": "shadowsocks", "tag": p.Tag, "server": p.Server,
		"server_port": p.Port, "method": p.Method, "password": p.Password,
	}
}

func buildTransport(p subscription.Proxy) map[string]interface{} {
	switch p.Network {
	case "ws":
		ws := map[string]interface{}{"type": "ws"}
		if p.WSPath != "" {
			ws["path"] = p.WSPath
		}
		if p.WSHost != "" {
			ws["headers"] = map[string]string{"Host": p.WSHost}
		}
		return ws
	case "grpc":
		g := map[string]interface{}{"type": "grpc"}
		if p.GRPCServiceName != "" {
			g["service_name"] = p.GRPCServiceName
		}
		return g
	}
	return nil
}
