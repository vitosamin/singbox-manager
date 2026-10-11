package ruleset

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// DefaultRulesetDir — путь по умолчанию на роутере.
const DefaultRulesetDir = "/opt/etc/sing-box/rulesets"

// CacheMaxAge — сколько хранить .srs в кэше без обновления.
// Если файл старше — пробуем скачать свежий.
const CacheMaxAge = 24 * time.Hour

// RulesetDir возвращает путь к папке со списками.
// Переопределяется переменной окружения RULESET_DIR.
func RulesetDir() string {
	if v := os.Getenv("RULESET_DIR"); v != "" {
		return v
	}
	return DefaultRulesetDir
}

// Download скачивает .srs по URL и сохраняет локально.
// name — ID списка (например, "youtube").
//
// Логика:
//   - Если локальный файл есть и свежий (< CacheMaxAge) — возвращаем его путь.
//   - Если файл есть, но старый — пробуем скачать свежий.
//   - Если скачивание упало, но есть кэш — возвращаем кэш + предупреждение.
//   - Если скачивание упало и кэша нет — возвращаем ("", nil), чтобы Apply
//     не отменялся (список просто не будет работать).
func Download(name, url string) (string, error) {
	if err := os.MkdirAll(RulesetDir(), 0755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", RulesetDir(), err)
	}

	localPath := filepath.Join(RulesetDir(), name+".srs")

	// 1. Проверяем кэш.
	if info, err := os.Stat(localPath); err == nil {
		age := time.Since(info.ModTime())
		if age < CacheMaxAge {
			fmt.Printf("[ruleset] %s: используем кэш (возраст %v)\n", name, age.Round(time.Minute))
			return localPath, nil
		}
		fmt.Printf("[ruleset] %s: кэш устарел (%v) — обновляем\n", name, age.Round(time.Hour))
	}

	// 2. Пробуем скачать.
	err := downloadFile(localPath, url)
	if err == nil {
		fmt.Printf("[ruleset] %s сохранён: %s\n", name, localPath)
		return localPath, nil
	}

	// 3. Скачивание упало — проверяем, есть ли кэш.
	if _, statErr := os.Stat(localPath); statErr == nil {
		fmt.Printf("[ruleset] %s: скачивание упало (%v), используем старый кэш\n", name, err)
		return localPath, nil
	}

	// 4. Кэша нет — возвращаем пустой путь, но НЕ ошибку.
	// Apply не отменяется, список просто не будет работать.
	fmt.Printf("[ruleset] %s: скачивание упало (%v), кэша нет — пропускаем список\n", name, err)
	return "", nil
}

// downloadFile — скачивает URL в localPath (через tmp + rename).
func downloadFile(localPath, url string) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("http.Get %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
	}

	tmpPath := localPath + ".tmp"
	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("Create %s: %w", tmpPath, err)
	}

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("io.Copy: %w", err)
	}
	tmpFile.Close()

	if err := os.Rename(tmpPath, localPath); err != nil {
		return fmt.Errorf("Rename: %w", err)
	}
	return nil
}

// DownloadByID скачивает список по его ID из каталога.
func DownloadByID(id string) (string, error) {
	item, ok := FindByID(id)
	if !ok {
		return "", fmt.Errorf("список %q не найден в каталоге", id)
	}
	return Download(item.ID, item.URL)
}

// ForceDownload — скачивает .srs, игнорируя кэш.
// Используется кнопкой «Скачать сейчас» в UI.
func ForceDownload(name, url string) (string, error) {
	if err := os.MkdirAll(RulesetDir(), 0755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", RulesetDir(), err)
	}

	localPath := filepath.Join(RulesetDir(), name+".srs")

	if err := downloadFile(localPath, url); err != nil {
		// Fallback на кэш, если есть.
		if _, statErr := os.Stat(localPath); statErr == nil {
			fmt.Printf("[ruleset] %s: force-download упал (%v), используем старый кэш\n", name, err)
			return localPath, nil
		}
		return "", err
	}
	fmt.Printf("[ruleset] %s сохранён (force): %s\n", name, localPath)
	return localPath, nil
}

// ForceDownloadByID — force-download по ID из каталога.
func ForceDownloadByID(id string) (string, error) {
	item, ok := FindByID(id)
	if !ok {
		return "", fmt.Errorf("список %q не найден в каталоге", id)
	}
	return ForceDownload(item.ID, item.URL)
}

// ListLocal возвращает список уже скачанных .srs.
func ListLocal() ([]string, error) {
	entries, err := os.ReadDir(RulesetDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var files []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".srs" {
			files = append(files, e.Name())
		}
	}
	return files, nil
}

// CacheStatus возвращает карту: имя файла → возраст.
// Используется в UI для индикатора 🟢/🟡/🔴.
func CacheStatus() (map[string]time.Duration, error) {
	entries, err := os.ReadDir(RulesetDir())
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]time.Duration{}, nil
		}
		return nil, err
	}

	result := make(map[string]time.Duration)
	now := time.Now()
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".srs" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		result[e.Name()] = now.Sub(info.ModTime())
	}
	return result, nil
}
