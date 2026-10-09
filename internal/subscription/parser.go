package subscription

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Parse принимает одну или несколько ссылок (разделённых переводом строки)
// и возвращает список распарсенных прокси.
func Parse(input string) ([]Proxy, error) {
	var result []Proxy
	counter := 1

	for _, line := range strings.Split(input, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		p, err := parseOne(line, counter)
		if err != nil {
			return nil, fmt.Errorf("строка %d: %w", counter, err)
		}
		if p.Name == "" {
			p.Name = fmt.Sprintf("Сервер %d", counter)
		}
		result = append(result, p)
		counter++
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("не найдено ни одной валидной ссылки")
	}

	return result, nil
}

func parseOne(raw string, idx int) (Proxy, error) {
	switch {
	case strings.HasPrefix(raw, "vless://"):
		return parseVLESS(raw, idx)
	case strings.HasPrefix(raw, "trojan://"):
		return parseTrojan(raw, idx)
	case strings.HasPrefix(raw, "hysteria2://"):
		return parseHysteria2(raw, idx)
	case strings.HasPrefix(raw, "ss://"):
		return parseShadowsocks(raw, idx)
	default:
		return Proxy{}, fmt.Errorf("неизвестный протокол: %.20s...", raw)
	}
}

func parseVLESS(raw string, idx int) (Proxy, error) {
	body := strings.TrimPrefix(raw, "vless://")
	u, err := url.Parse("vless://" + body)
	if err != nil {
		return Proxy{}, fmt.Errorf("url.Parse: %w", err)
	}

	uuid := u.User.Username()
	host := u.Hostname()
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		return Proxy{}, fmt.Errorf("некорректный порт: %w", err)
	}

	q := u.Query()
	p := Proxy{
		Tag:             fmt.Sprintf("proxy-%d", idx),
		Protocol:        "vless",
		Server:          host,
		Port:            port,
		UUID:            uuid,
		Security:        q.Get("security"),
		SNI:             q.Get("sni"),
		Flow:            q.Get("flow"),
		Network:         q.Get("type"),
		Fingerprint:     q.Get("fp"),
		PublicKey:       q.Get("pbk"),
		ShortID:         q.Get("sid"),
		SpiderX:         q.Get("spx"),
		WSPath:          q.Get("path"),
		WSHost:          q.Get("host"),
		GRPCServiceName: q.Get("serviceName"),
		Raw:             raw,
	}

	if p.Security == "" {
		p.Security = "none"
	}
	if p.Network == "" {
		p.Network = "tcp"
	}
	if alpn := q.Get("alpn"); alpn != "" {
		p.ALPN = strings.Split(alpn, ",")
	}

	if remark := u.Fragment; remark != "" {
		// URL-декодируем (там emoji флаги)
		if decoded, err := url.QueryUnescape(remark); err == nil {
			p.Remark = decoded
			p.Name = decoded
		} else {
			p.Remark = remark
			p.Name = remark
		}
	}

	if p.UUID == "" {
		return Proxy{}, fmt.Errorf("отсутствует UUID")
	}
	if p.Server == "" {
		return Proxy{}, fmt.Errorf("отсутствует адрес сервера")
	}

	return p, nil
}

func parseTrojan(raw string, idx int) (Proxy, error) {
	body := strings.TrimPrefix(raw, "trojan://")
	u, err := url.Parse("trojan://" + body)
	if err != nil {
		return Proxy{}, fmt.Errorf("url.Parse: %w", err)
	}

	password := u.User.Username()
	host := u.Hostname()
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		return Proxy{}, fmt.Errorf("некорректный порт: %w", err)
	}

	q := u.Query()
	p := Proxy{
		Tag:      fmt.Sprintf("proxy-%d", idx),
		Protocol: "trojan",
		Server:   host,
		Port:     port,
		Password: password,
		SNI:      q.Get("sni"),
		Network:  q.Get("type"),
		WSPath:   q.Get("path"),
		WSHost:   q.Get("host"),
		Raw:      raw,
	}

	if p.SNI == "" {
		p.SNI = host
	}
	if p.Network == "" {
		p.Network = "tcp"
	}
	if remark := u.Fragment; remark != "" {
		p.Remark = remark
		p.Name = remark
	}

	return p, nil
}

func parseHysteria2(raw string, idx int) (Proxy, error) {
	body := strings.TrimPrefix(raw, "hysteria2://")
	u, err := url.Parse("hysteria2://" + body)
	if err != nil {
		return Proxy{}, fmt.Errorf("url.Parse: %w", err)
	}

	var password string
	if u.User != nil {
		password = u.User.Username()
		if pw, ok := u.User.Password(); ok && pw != "" {
			password = pw
		}
	}

	host := u.Hostname()
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		return Proxy{}, fmt.Errorf("некорректный порт: %w", err)
	}

	q := u.Query()
	p := Proxy{
		Tag:          fmt.Sprintf("proxy-%d", idx),
		Protocol:     "hysteria2",
		Server:       host,
		Port:         port,
		Password:     password,
		SNI:          q.Get("sni"),
		Obfs:         q.Get("obfs"),
		ObfsPassword: q.Get("obfs-password"),
		Raw:          raw,
	}

	if p.SNI == "" {
		p.SNI = host
	}
	if remark := u.Fragment; remark != "" {
		p.Remark = remark
		p.Name = remark
	}

	return p, nil
}

func parseShadowsocks(raw string, idx int) (Proxy, error) {
	body := strings.TrimPrefix(raw, "ss://")

	var remark string
	if i := strings.Index(body, "#"); i != -1 {
		remark, _ = url.QueryUnescape(body[i+1:])
		body = body[:i]
	}

	atIdx := strings.LastIndex(body, "@")
	if atIdx == -1 {
		return Proxy{}, fmt.Errorf("некорректный формат ss://")
	}

	userInfo := body[:atIdx]
	hostPort := body[atIdx+1:]

	var method, password string
	if decoded, err := base64.RawURLEncoding.DecodeString(userInfo); err == nil {
		parts := strings.SplitN(string(decoded), ":", 2)
		if len(parts) == 2 {
			method = parts[0]
			password = parts[1]
		}
	}
	if method == "" {
		parts := strings.SplitN(userInfo, ":", 2)
		if len(parts) == 2 {
			method = parts[0]
			password = parts[1]
		}
	}

	hostPort = strings.TrimSuffix(hostPort, "/")
	lastColon := strings.LastIndex(hostPort, ":")
	if lastColon == -1 {
		return Proxy{}, fmt.Errorf("некорректный host:port")
	}
	host := hostPort[:lastColon]
	port, err := strconv.Atoi(hostPort[lastColon+1:])
	if err != nil {
		return Proxy{}, fmt.Errorf("некорректный порт: %w", err)
	}

	p := Proxy{
		Tag:      fmt.Sprintf("proxy-%d", idx),
		Protocol: "shadowsocks",
		Server:   host,
		Port:     port,
		Method:   method,
		Password: password,
		Remark:   remark,
		Name:     remark,
		Raw:      raw,
	}

	return p, nil
}
