package config

// DeviceRule — правило маршрутизации для конкретного устройства по IP.
type DeviceRule struct {
	ID       string `json:"id"`        // внутренний id (для UI)
	IP       string `json:"ip"`        // 192.168.1.100
	Outbound string `json:"outbound"`  // "proxy-default" | "direct" | "block" | "proxy-youtube" и т.д.
	Enabled  bool   `json:"enabled"`   // вкл/выкл правило
	Remark   string `json:"remark"`    // описание (имя устройства)
}

// BuildDeviceRules возвращает список route.rules для устройств.
func BuildDeviceRules(devices []DeviceRule) []map[string]interface{} {
	var out []map[string]interface{}
	for _, d := range devices {
		if !d.Enabled || d.IP == "" || d.Outbound == "" {
			continue
		}
		out = append(out, map[string]interface{}{
			"ip_cidr": []string{d.IP + "/32"},
			"outbound": d.Outbound,
		})
	}
	return out
}
