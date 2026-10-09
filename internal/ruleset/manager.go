package ruleset

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// DefaultRulesetDir — путь по умолчанию на роутере.
const DefaultRulesetDir = "/opt/etc/sing-box/rulesets"

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
func Download(name, url string) (string, error) {
	if err := os.MkdirAll(RulesetDir(), 0755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", RulesetDir(), err)
	}

	localPath := filepath.Join(RulesetDir(), name+".srs")

	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("http.Get %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
	}

	tmpPath := localPath + ".tmp"
	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		return "", fmt.Errorf("Create %s: %w", tmpPath, err)
	}

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("io.Copy: %w", err)
	}
	tmpFile.Close()

	if err := os.Rename(tmpPath, localPath); err != nil {
		return "", fmt.Errorf("Rename: %w", err)
	}

	fmt.Printf("[ruleset] %s сохранён: %s\n", name, localPath)
	return localPath, nil
}

// DownloadByID скачивает список по его ID из каталога.
func DownloadByID(id string) (string, error) {
	item, ok := FindByID(id)
	if !ok {
		return "", fmt.Errorf("список %q не найден в каталоге", id)
	}
	return Download(item.ID, item.URL)
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
