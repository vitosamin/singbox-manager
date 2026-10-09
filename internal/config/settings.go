package config

// Settings — пользовательские настройки приложения.
type Settings struct {
	// LogEnabled — писать ли логи Sing-box в Ring-буфер (RAM).
	// Если false — stdout/stderr Sing-box выбрасывается.
	LogEnabled bool `json:"log_enabled"`
	// LogRingSize — размер буфера в строках.
	LogRingSize int `json:"log_ring_size"`
	// AutoSyncEnabled — включено ли автообновление .srs-файлов.
	AutoSyncEnabled bool `json:"auto_sync_enabled"`
	// AutoSyncInterval — интервал в часах (1, 3, 6, 12, 24).
	AutoSyncInterval int `json:"auto_sync_interval"`
	// RestartOnApply — перезапускать ли Sing-box после «Применить».
	RestartOnApply bool `json:"restart_on_apply"`
}

// DefaultSettings — дефолтные значения.
func DefaultSettings() Settings {
	return Settings{
		LogEnabled:       true,
		LogRingSize:      2000,
		AutoSyncEnabled:  false,
		AutoSyncInterval: 24,
		RestartOnApply:   true,
	}
}
