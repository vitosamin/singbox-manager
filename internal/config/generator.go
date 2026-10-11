package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/shapovalenko/keenetic-singbox-manager/internal/ruleset"
	"github.com/shapovalenko/keenetic-singbox-manager/internal/subscription"
)

const BasePort = 1080
const TProxyPort = 1081
const RedirectPort = 1082
const ConfigDirName = "config.d"

func ConfigDir(installDir string) string {
	return filepath.Join(installDir, ConfigDirName)
}

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

	tunnels, err := buildTunnels(proxies, groups, enabledIDs, customSRS)
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

func buildTunnels(proxies []subscription.Proxy, groups GroupConfig, enabledIDs []string, customSRS []ruleset.CustomSRS) (map[string]interface{}, error) {
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

	outbounds := make([]map[string]interface{}, 0, len(proxies))
	for _, p := range proxies {
		ob := proxyToOutbound(p)
		if ob == nil {
			return nil, fmt.Errorf("неподдерживаемый протокол: %s", p.Protocol)
		}
		ob["tag"] = p.Tag
		outbounds = append(outbounds, ob)
	}

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
	}

	ruleSets := make([]map[string]interface{}, 0, len(enabledIDs)+len(customSRS))
	rules := make([]map[string]interface{}, 0, len(enabledIDs)+len(customSRS))

	for _, id := range enabledIDs {
		path := "/opt/etc/sing-box/rulesets/" + id + ".srs"
		if _, err := os.Stat(path); err != nil {
			fmt.Printf("[config] пропущен %s (нет файла)\n", id)
			continue
		}
		ruleSets = append(ruleSets, map[string]interface{}{
			"type":   "local",
			"tag":    id,
			"format": "binary",
			"path":   path,
		})
		rules = append(rules, map[string]interface{}{
			"rule_set": []string{id},
			"outbound": "proxy-" + id,
		})
	}

	for _, srs := range customSRS {
		path := "/opt/etc/sing-box/rulesets/" + srs.ID + ".srs"
		if _, err := os.Stat(path); err != nil {
			fmt.Printf("[config] пропущен %s (нет файла)\n", srs.ID)
			continue
		}
		ruleSets = append(ruleSets, map[string]interface{}{
			"type":   "local",
			"tag":    srs.ID,
			"format": "binary",
			"path":   path,
		})
		rules = append(rules, map[string]interface{}{
			"rule_set": []string{srs.ID},
			"outbound": "proxy-" + srs.ID,
		})
	}

	fmt.Printf("[config] rule_set: %d, rules: %d, final: direct\n", len(ruleSets), len(rules))

	route := map[string]interface{}{
		"rules": rules,
		"final": "direct",
	}
	if len(ruleSets) > 0 {
		route["rule_set"] = ruleSets
	}

	return map[string]interface{}{
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"route":     route,
	}, nil
}

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

func writeJSONFile(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

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
	return GenerateV3(enabledIDs, customRules, customSRS, proxies, groups, dnsCfg, devices, installDir)
}

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
