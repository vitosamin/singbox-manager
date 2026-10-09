package config

// GroupProxy — один прокси в составе группы с флагом включения.
type GroupProxy struct {
	Tag     string `json:"tag"`
	Enabled bool   `json:"enabled"`
}

// Group — группа прокси с порядком приоритета.
type Group struct {
	Proxies []GroupProxy `json:"proxies"`
	// Manual — true, если группа создана вручную пользователем.
	// Такие группы не удаляются автоматически при syncAllGroups.
	Manual bool `json:"manual,omitempty"`
	// Label — человекочитаемое название (для ручных групп).
	Label string `json:"label,omitempty"`
}

// GroupConfig — раскладка: ключ = ID группы.
type GroupConfig map[string]Group

// PriorityTags возвращает только включённые теги в порядке приоритета.
func (g Group) PriorityTags() []string {
	var out []string
	for _, p := range g.Proxies {
		if p.Enabled {
			out = append(out, p.Tag)
		}
	}
	return out
}

// AllTags возвращает все теги (включая выключенные).
func (g Group) AllTags() []string {
	var out []string
	for _, p := range g.Proxies {
		out = append(out, p.Tag)
	}
	return out
}

// SetEnabled включает/выключает прокси по тегу.
func (g *Group) SetEnabled(tag string, enabled bool) {
	for i := range g.Proxies {
		if g.Proxies[i].Tag == tag {
			g.Proxies[i].Enabled = enabled
			return
		}
	}
	g.Proxies = append(g.Proxies, GroupProxy{Tag: tag, Enabled: enabled})
}

// Move перемещает прокси вверх/вниз.
func (g *Group) Move(from, to int) {
	if from < 0 || from >= len(g.Proxies) || to < 0 || to >= len(g.Proxies) {
		return
	}
	g.Proxies[from], g.Proxies[to] = g.Proxies[to], g.Proxies[from]
}

// EnsureTags гарантирует, что все переданные теги есть в группе.
func (g *Group) EnsureTags(allTags []string, defaultEnabled bool) {
	existing := map[string]bool{}
	for _, p := range g.Proxies {
		existing[p.Tag] = true
	}
	for _, t := range allTags {
		if !existing[t] {
			g.Proxies = append(g.Proxies, GroupProxy{Tag: t, Enabled: defaultEnabled})
		}
	}
}
