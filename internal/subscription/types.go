package subscription

// Proxy — универсальное представление распарсенной ссылки на сервер.
type Proxy struct {
	// Идентификация
	Tag  string `json:"tag"`  // внутренний тег для Sing-box (proxy-1, proxy-2, ...)
	Name string `json:"name"` // человекочитаемое имя (из #remark или авто)
	Protocol string `json:"protocol"`
	Server   string `json:"server"`
	Port     int    `json:"port"`
	Remark   string `json:"remark"`

	// VLESS / VMess / Trojan
	UUID        string   `json:"uuid,omitempty"`
	Flow        string   `json:"flow,omitempty"`
	Security    string   `json:"security,omitempty"`
	SNI         string   `json:"sni,omitempty"`
	ALPN        []string `json:"alpn,omitempty"`
	Fingerprint string   `json:"fingerprint,omitempty"`

	// Reality
	PublicKey string `json:"public_key,omitempty"`
	ShortID   string `json:"short_id,omitempty"`
	SpiderX   string `json:"spider_x,omitempty"`

	// Транспорт
	Network         string `json:"network,omitempty"`
	WSPath          string `json:"ws_path,omitempty"`
	WSHost          string `json:"ws_host,omitempty"`
	GRPCServiceName string `json:"grpc_service_name,omitempty"`

	// Hysteria2
	Password     string `json:"password,omitempty"`
	Obfs         string `json:"obfs,omitempty"`
	ObfsPassword string `json:"obfs_password,omitempty"`

	// Shadowsocks
	Method string `json:"method,omitempty"`

	Raw string `json:"raw,omitempty"`
}
