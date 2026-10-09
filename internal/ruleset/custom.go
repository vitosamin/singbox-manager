package ruleset

// CustomRule — пользовательское правило (домены + CIDR).
type CustomRule struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Domains  []string `json:"domains"`
	IPCIDRs  []string `json:"ip_cidrs"`
	// Outbound — явное указание, куда направить трафик.
	// Пусто = использовать группу с тем же ID (как раньше).
	// "direct", "block", "proxy-default", "proxy-youtube" и т.д.
	Outbound string   `json:"outbound,omitempty"`
	// Enabled — включено ли правило.
	Enabled  bool     `json:"enabled"`
}

// CustomSRS — пользовательская ссылка на .srs файл.
type CustomSRS struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Outbound string `json:"outbound,omitempty"`
	Enabled  bool   `json:"enabled"`
}

// FindCustomByID ищет правило в списке.
func FindCustomByID(rules []CustomRule, id string) (CustomRule, bool) {
	for _, r := range rules {
		if r.ID == id {
			return r, true
		}
	}
	return CustomRule{}, false
}

// FindCustomSRSByID ищет пользовательский .srs по ID.
func FindCustomSRSByID(items []CustomSRS, id string) (CustomSRS, bool) {
	for _, r := range items {
		if r.ID == id {
			return r, true
		}
	}
	return CustomSRS{}, false
}
