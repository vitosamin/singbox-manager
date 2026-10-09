package config

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/shapovalenko/keenetic-singbox-manager/internal/ruleset"
	"github.com/shapovalenko/keenetic-singbox-manager/internal/subscription"
)

type RuleSet struct {
	Type   string                   `json:"type"`
	Tag    string                   `json:"tag"`
	Format string                   `json:"format,omitempty"`
	Path   string                   `json:"path,omitempty"`
	Rules  []map[string]interface{} `json:"rules,omitempty"`
}

type RouteRule struct {
	RuleSet  []string `json:"rule_set,omitempty"`
	Outbound string   `json:"outbound,omitempty"`
	IPCIDR   []string `json:"ip_cidr,omitempty"`
}

type Route struct {
	RuleSet               []RuleSet              `json:"rule_set,omitempty"`
	Rules                 []RouteRule            `json:"rules,omitempty"`
	Final                 string                 `json:"final"`
	AutoDetectInterface   bool                   `json:"auto_detect_interface,omitempty"`
	DefaultDomainResolver map[string]interface{} `json:"default_domain_resolver,omitempty"`
}

type Experimental struct {
	ClashAPI ClashAPI `json:"clash_api"`
}

type ClashAPI struct {
	ExternalController string `json:"external_controller"`
	Secret             string `json:"secret,omitempty"`
}

type Config struct {
	Log          map[string]interface{}   `json:"log,omitempty"`
	DNS          map[string]interface{}   `json:"dns,omitempty"`
	Inbounds     []map[string]interface{} `json:"inbounds,omitempty"`
	Outbounds    []map[string]interface{} `json:"outbounds,omitempty"`
	Route        Route                    `json:"route,omitempty"`
	Experimental Experimental             `json:"experimental,omitempty"`
}

const ClashAPIPort = 9090

func resolveTargetGroup(groups GroupConfig, tag string) string {
	if g, ok := groups[tag]; ok && len(g.PriorityTags()) > 0 {
		return "proxy-" + tag
	}
	return "proxy-default"
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
	ruleSets, ruleTags := buildRuleSets(enabledIDs)
	customSets, customTags := buildCustomRuleSets(customRules)
	srsSets, srsTags := buildCustomSRSSets(customSRS)
	ruleSets = append(ruleSets, customSets...)
	ruleSets = append(ruleSets, srsSets...)

	outbounds := buildOutbounds(proxies, groups)

	var rules []RouteRule

	// 1. Устройства
	for _, dr := range BuildDeviceRules(devices) {
		ipcidr, _ := dr["ip_cidr"].([]string)
		outbound, _ := dr["outbound"].(string)
		rules = append(rules, RouteRule{
			IPCIDR:   ipcidr,
			Outbound: outbound,
		})
	}

	// 2. Каталог
	for _, tag := range ruleTags {
		rules = append(rules, RouteRule{
			RuleSet:  []string{tag},
			Outbound: resolveTargetGroup(groups, tag),
		})
	}

	// 3. Свои правила
	for _, tag := range customTags {
		r := findCustomRule(customRules, tag)
		outbound := resolveTargetGroup(groups, tag)
		if r != nil && r.Outbound != "" {
			outbound = r.Outbound
		}
		rules = append(rules, RouteRule{
			RuleSet:  []string{tag},
			Outbound: outbound,
		})
	}

	// 4. Свои .srs
	for _, tag := range srsTags {
		s := findCustomSRS(customSRS, tag)
		outbound := resolveTargetGroup(groups, tag)
		if s != nil && s.Outbound != "" {
			outbound = s.Outbound
		}
		rules = append(rules, RouteRule{
			RuleSet:  []string{tag},
			Outbound: outbound,
		})
	}

	finalOutbound := "direct"
	if g, ok := groups["default"]; ok && len(g.PriorityTags()) > 0 {
		finalOutbound = "proxy-default"
	}

	if err := dnsCfg.Validate(); err != nil {
		return fmt.Errorf("dns config: %w", err)
	}

	proxyDetour := ""
	if g, ok := groups["default"]; ok && len(g.PriorityTags()) > 0 {
		proxyDetour = "proxy-default"
	}

	dnsSection := dnsCfg.BuildDNS(proxyDetour)

	cfg := Config{
		Log:       map[string]interface{}{"level": "info", "timestamp": true},
		DNS:       dnsSection,
		Inbounds:  buildInbounds(),
		Outbounds: outbounds,
		Route: Route{
			RuleSet: ruleSets,
			Rules:   rules,
			Final:   finalOutbound,
			// auto_detect_interface предотвращает петлю трафика при auto_route
			AutoDetectInterface: true,
			DefaultDomainResolver: map[string]interface{}{
				"server": dnsCfg.DefaultServer,
			},
		},
		Experimental: Experimental{
			ClashAPI: ClashAPI{
				ExternalController: fmt.Sprintf("127.0.0.1:%d", ClashAPIPort),
			},
		},
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	if err := os.WriteFile(outPath, data, 0644); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}

	fmt.Printf("[config] записан %s\n", outPath)
	fmt.Printf("  списков: %d предуст + %d custom + %d srs\n", len(enabledIDs), len(customRules), len(customSRS))
	fmt.Printf("  устройств: %d, outbounds: %d, групп: %d\n", len(devices), len(outbounds), len(groups))
	return nil
}

func findCustomRule(rules []ruleset.CustomRule, id string) *ruleset.CustomRule {
	for i := range rules {
		if rules[i].ID == id {
			return &rules[i]
		}
	}
	return nil
}

func findCustomSRS(items []ruleset.CustomSRS, id string) *ruleset.CustomSRS {
	for i := range items {
		if items[i].ID == id {
			return &items[i]
		}
	}
	return nil
}

func buildRuleSets(ids []string) ([]RuleSet, []string) {
	var ruleSets []RuleSet
	var tags []string
	for _, id := range ids {
		item, ok := ruleset.FindByID(id)
		if !ok {
			continue
		}
		ruleSets = append(ruleSets, RuleSet{
			Type: "local", Tag: item.ID, Format: "binary",
			Path: ruleset.RulesetDir() + "/" + item.ID + ".srs",
		})
		tags = append(tags, item.ID)
	}
	return ruleSets, tags
}

func buildCustomRuleSets(rules []ruleset.CustomRule) ([]RuleSet, []string) {
	var ruleSets []RuleSet
	var tags []string
	for _, r := range rules {
		if len(r.Domains) == 0 && len(r.IPCIDRs) == 0 {
			continue
		}
		if !r.Enabled {
			continue
		}
		var inlineRules []map[string]interface{}
		if len(r.Domains) > 0 {
			inlineRules = append(inlineRules, map[string]interface{}{"domain_suffix": r.Domains})
		}
		if len(r.IPCIDRs) > 0 {
			inlineRules = append(inlineRules, map[string]interface{}{"ip_cidr": r.IPCIDRs})
		}
		ruleSets = append(ruleSets, RuleSet{Type: "inline", Tag: r.ID, Rules: inlineRules})
		tags = append(tags, r.ID)
	}
	return ruleSets, tags
}

func buildCustomSRSSets(items []ruleset.CustomSRS) ([]RuleSet, []string) {
	var ruleSets []RuleSet
	var tags []string
	for _, s := range items {
		if !s.Enabled {
			continue
		}
		ruleSets = append(ruleSets, RuleSet{
			Type: "local", Tag: s.ID, Format: "binary",
			Path: ruleset.RulesetDir() + "/" + s.ID + ".srs",
		})
		tags = append(tags, s.ID)
	}
	return ruleSets, tags
}

func buildOutbounds(proxies []subscription.Proxy, groups GroupConfig) []map[string]interface{} {
	proxyMap := make(map[string]subscription.Proxy)
	for _, p := range proxies {
		proxyMap[p.Tag] = p
	}
	var outbounds []map[string]interface{}
	for groupName, g := range groups {
		tags := g.PriorityTags()
		var validTags []string
		for _, t := range tags {
			if _, ok := proxyMap[t]; ok {
				validTags = append(validTags, t)
			}
		}
		if len(validTags) == 0 {
			continue
		}
		outbounds = append(outbounds, map[string]interface{}{
			"type": "selector", "tag": "proxy-" + groupName,
			"outbounds": validTags, "default": validTags[0],
		})
	}
	if len(groups) == 0 && len(proxies) > 0 {
		var allTags []string
		for _, p := range proxies {
			allTags = append(allTags, p.Tag)
		}
		outbounds = append(outbounds, map[string]interface{}{
			"type": "selector", "tag": "proxy-default",
			"outbounds": allTags, "default": allTags[0],
		})
	}
	for _, p := range proxies {
		ob := proxyToOutbound(p)
		if ob != nil {
			outbounds = append(outbounds, ob)
		}
	}
	outbounds = append(outbounds,
		map[string]interface{}{"type": "direct", "tag": "direct"},
		map[string]interface{}{"type": "block", "tag": "block"},
	)
	return outbounds
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
	if p.Flow != "" {
		ob["flow"] = p.Flow
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

// buildInbounds — TUN с новым форматом адресов (sing-box 1.12+).
//   address вместо inet4_address — старый формат удалён
//   auto_redirect — на Linux лучше auto_route
//   stack: system — меньше памяти
func buildInbounds() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"type":           "tun",
			"tag":            "tun-in",
			"interface_name": "singtun0",
			// Новый формат: address — массив CIDR
			"address":        []string{"172.19.0.1/30"},
			"mtu":            1500,
			"auto_route":     true,
			"auto_redirect":  true,
			"strict_route":   false,
			"stack":          "system",
		},
	}
}
