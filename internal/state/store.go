package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/shapovalenko/keenetic-singbox-manager/internal/config"
	"github.com/shapovalenko/keenetic-singbox-manager/internal/ruleset"
	"github.com/shapovalenko/keenetic-singbox-manager/internal/subscription"
)

type State struct {
	Subscription string               `json:"subscription"`
	Proxies      []subscription.Proxy `json:"proxies"`
	EnabledLists []string             `json:"enabled_lists"`
	Groups       config.GroupConfig   `json:"groups"`
	CustomRules  []ruleset.CustomRule `json:"custom_rules"`
	CustomSRS    []ruleset.CustomSRS  `json:"custom_srs"`
	DNS          config.DNSConfig     `json:"dns"`
	Devices      []config.DeviceRule  `json:"devices"`
	Theme        string               `json:"theme"`
	Settings     config.Settings      `json:"settings"`
	PasswordHash string               `json:"password_hash"`
}

type Store struct {
	Path  string
	mu    sync.Mutex
	state *State
}

const DefaultPath = "/opt/etc/sing-box/state.json"

func NewStore() *Store {
	path := os.Getenv("STATE_FILE")
	if path == "" {
		path = DefaultPath
	}
	return &Store{
		Path: path,
		state: &State{
			Groups:   config.GroupConfig{},
			DNS:      config.DefaultDNSConfig(),
			Devices:  []config.DeviceRule{},
			Theme:    "dark",
			Settings: config.DefaultSettings(),
		},
	}
}

func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", s.Path, err)
	}

	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	if st.Groups == nil {
		st.Groups = config.GroupConfig{}
	}
	if st.CustomRules == nil {
		st.CustomRules = []ruleset.CustomRule{}
	}
	if st.CustomSRS == nil {
		st.CustomSRS = []ruleset.CustomSRS{}
	}
	if st.Devices == nil {
		st.Devices = []config.DeviceRule{}
	}
	if st.Theme == "" {
		st.Theme = "dark"
	}
	if len(st.DNS.Servers) == 0 {
		st.DNS = config.DefaultDNSConfig()
	}
	if st.Settings.LogRingSize == 0 {
		st.Settings = config.DefaultSettings()
	}
	s.state = &st
	s.syncAllGroupsLocked()
	return nil
}

func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmpPath := s.Path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmpPath, s.Path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func (s *Store) Snapshot() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := *s.state
	st.EnabledLists = append([]string{}, s.state.EnabledLists...)
	st.Proxies = append([]subscription.Proxy{}, s.state.Proxies...)
	st.CustomRules = append([]ruleset.CustomRule{}, s.state.CustomRules...)
	st.CustomSRS = append([]ruleset.CustomSRS{}, s.state.CustomSRS...)
	st.Devices = append([]config.DeviceRule{}, s.state.Devices...)
	if s.state.Groups != nil {
		st.Groups = config.GroupConfig{}
		for k, g := range s.state.Groups {
			ng := config.Group{
				Proxies: append([]config.GroupProxy{}, g.Proxies...),
				Manual:  g.Manual,
				Label:   g.Label,
			}
			st.Groups[k] = ng
		}
	}
	st.DNS = s.state.DNS
	st.DNS.Servers = append([]config.DNSServer{}, s.state.DNS.Servers...)
	st.DNS.Rules = append([]config.DNSRule{}, s.state.DNS.Rules...)
	return st
}

func (s *Store) syncAllGroupsLocked() {
	autoGroupNames := map[string]bool{"default": true}
	for _, id := range s.state.EnabledLists {
		autoGroupNames[id] = true
	}
	for _, r := range s.state.CustomRules {
		if r.ID != "" {
			autoGroupNames[r.ID] = true
		}
	}
	for _, sr := range s.state.CustomSRS {
		if sr.ID != "" {
			autoGroupNames[sr.ID] = true
		}
	}
	for name, g := range s.state.Groups {
		if g.Manual {
			continue
		}
		if !autoGroupNames[name] {
			delete(s.state.Groups, name)
		}
	}
	allTags := make([]string, 0, len(s.state.Proxies))
	for _, p := range s.state.Proxies {
		allTags = append(allTags, p.Tag)
	}
	for name := range autoGroupNames {
		g, ok := s.state.Groups[name]
		if !ok {
			g = config.Group{}
		}
		existing := map[string]bool{}
		for _, t := range allTags {
			existing[t] = true
		}
		var kept []config.GroupProxy
		for _, gp := range g.Proxies {
			if existing[gp.Tag] {
				kept = append(kept, gp)
			}
		}
		g.Proxies = kept
		g.EnsureTags(allTags, true)
		s.state.Groups[name] = g
	}
	for name, g := range s.state.Groups {
		if !g.Manual {
			continue
		}
		existing := map[string]bool{}
		for _, t := range allTags {
			existing[t] = true
		}
		var kept []config.GroupProxy
		for _, gp := range g.Proxies {
			if existing[gp.Tag] {
				kept = append(kept, gp)
			}
		}
		g.Proxies = kept
		g.EnsureTags(allTags, false)
		s.state.Groups[name] = g
	}
}

func (s *Store) SetSubscription(text string, proxies []subscription.Proxy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Subscription = text
	s.state.Proxies = proxies
	s.syncAllGroupsLocked()
	return s.saveLocked()
}

func (s *Store) SetEnabledLists(ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.EnabledLists = append([]string{}, ids...)
	s.syncAllGroupsLocked()
	return s.saveLocked()
}

func (s *Store) SetGroups(g config.GroupConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range g {
		if old, ok := s.state.Groups[k]; ok {
			if v.Manual == false {
				v.Manual = old.Manual
			}
			if v.Label == "" {
				v.Label = old.Label
			}
			g[k] = v
		}
	}
	s.state.Groups = config.GroupConfig{}
	for k, v := range g {
		ng := config.Group{
			Proxies: append([]config.GroupProxy{}, v.Proxies...),
			Manual:  v.Manual,
			Label:   v.Label,
		}
		s.state.Groups[k] = ng
	}
	return s.saveLocked()
}

func (s *Store) AddManualGroup(name, label string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.state.Groups[name]; exists {
		return fmt.Errorf("группа %q уже существует", name)
	}
	allTags := []string{}
	for _, p := range s.state.Proxies {
		allTags = append(allTags, p.Tag)
	}
	g := config.Group{Manual: true, Label: label}
	g.EnsureTags(allTags, false)
	s.state.Groups[name] = g
	return s.saveLocked()
}

func (s *Store) RemoveManualGroup(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.state.Groups[name]
	if !ok {
		return fmt.Errorf("группа %q не найдена", name)
	}
	if !g.Manual {
		return fmt.Errorf("группа %q не ручная, нельзя удалить", name)
	}
	delete(s.state.Groups, name)
	return s.saveLocked()
}

func (s *Store) SetCustomRules(rules []ruleset.CustomRule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range rules {
		if rules[i].ID == "" {
			rules[i].ID = fmt.Sprintf("custom-%d", i+1)
		}
	}
	s.state.CustomRules = append([]ruleset.CustomRule{}, rules...)
	s.syncAllGroupsLocked()
	return s.saveLocked()
}

func (s *Store) SetCustomSRS(items []ruleset.CustomSRS) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range items {
		if items[i].ID == "" {
			items[i].ID = fmt.Sprintf("srs-%d", i+1)
		}
	}
	s.state.CustomSRS = append([]ruleset.CustomSRS{}, items...)
	s.syncAllGroupsLocked()
	return s.saveLocked()
}

func (s *Store) SetDNS(cfg config.DNSConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.DNS = cfg
	return s.saveLocked()
}

func (s *Store) SetDevices(devices []config.DeviceRule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Devices = append([]config.DeviceRule{}, devices...)
	return s.saveLocked()
}

func (s *Store) SetTheme(theme string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Theme = theme
	return s.saveLocked()
}

func (s *Store) SetSettings(settings config.Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Settings = settings
	return s.saveLocked()
}

func (s *Store) SetPasswordHash(hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.PasswordHash = hash
	return s.saveLocked()
}

func (s *Store) PasswordHash() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.PasswordHash
}

func (s *Store) AddProxy(raw string) ([]subscription.Proxy, error) {
	parsed, err := subscription.Parse(raw)
	if err != nil {
		return nil, err
	}
	if len(parsed) == 0 {
		return nil, fmt.Errorf("не распознано ни одной ссылки")
	}
	nextIdx := len(s.state.Proxies) + 1
	for i := range parsed {
		parsed[i].Tag = fmt.Sprintf("proxy-%d", nextIdx+i)
	}
	s.mu.Lock()
	s.state.Proxies = append(s.state.Proxies, parsed...)
	s.syncAllGroupsLocked()
	err = s.saveLocked()
	s.mu.Unlock()
	return parsed, err
}

func (s *Store) RemoveProxy(tag string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var kept []subscription.Proxy
	for _, p := range s.state.Proxies {
		if p.Tag != tag {
			kept = append(kept, p)
		}
	}
	s.state.Proxies = kept
	for name, g := range s.state.Groups {
		var newProxies []config.GroupProxy
		for _, gp := range g.Proxies {
			if gp.Tag != tag {
				newProxies = append(newProxies, gp)
			}
		}
		g.Proxies = newProxies
		s.state.Groups[name] = g
	}
	return s.saveLocked()
}

func (s *Store) RawJSON() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return json.MarshalIndent(s.state, "", "  ")
}

func (s *Store) ReplaceFromJSON(data []byte) error {
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	if st.Groups == nil { st.Groups = config.GroupConfig{} }
	if st.CustomRules == nil { st.CustomRules = []ruleset.CustomRule{} }
	if st.CustomSRS == nil { st.CustomSRS = []ruleset.CustomSRS{} }
	if st.Devices == nil { st.Devices = []config.DeviceRule{} }
	if st.Theme == "" { st.Theme = "dark" }
	if len(st.DNS.Servers) == 0 { st.DNS = config.DefaultDNSConfig() }
	if st.Settings.LogRingSize == 0 { st.Settings = config.DefaultSettings() }
	s.mu.Lock()
	s.state = &st
	s.syncAllGroupsLocked()
	err := s.saveLocked()
	s.mu.Unlock()
	return err
}
