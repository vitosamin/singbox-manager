package singbox

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/shapovalenko/keenetic-singbox-manager/internal/logger"
)

const (
	// Version — версия бинарника Sing-box (AWG-сборка).
	Version           = "1.15.0-alpha.10-awgm.31"
	DefaultInstallDir = "/opt/etc/sing-box"
)

var LogRing = logger.NewRing(2000)
var logEnabled atomic.Bool

func init() {
	logEnabled.Store(true)
}

func SetLogEnabled(enabled bool) { logEnabled.Store(enabled) }
func SetLogRingSize(size int)    { LogRing = logger.NewRing(size) }

func InstallDir() string {
	if v := os.Getenv("SINGBOX_DIR"); v != "" {
		return v
	}
	return DefaultInstallDir
}

// BinaryPath — путь к бинарнику Sing-box.
func BinaryPath() string { return InstallDir() + "/sing-box" }

// ConfigDir — путь к каталогу config.d (для `-C`).
func ConfigDir() string { return InstallDir() + "/config.d" }

// LoaderPath — путь к glibc-загрузчику для Keenetic.
func LoaderPath() string {
	for _, p := range []string{"/lib/ld-linux-aarch64.so.1", "/opt/lib/ld-linux-aarch64.so.1"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// singBoxArgs — собираем команду запуска с учётом загрузчика.
func singBoxArgs(subcommand ...string) []string {
	bin := BinaryPath()
	if _, err := os.Stat("/lib/ld-linux-aarch64.so.1"); err == nil {
		return append([]string{bin}, subcommand...)
	}
	if loader := LoaderPath(); loader != "" {
		return append([]string{loader, bin}, subcommand...)
	}
	return append([]string{bin}, subcommand...)
}

// EnsureInstalled — проверяет, что бинарник Sing-box установлен.
// В v3.0.0 бинарник скачивается отдельно (из нашего GitHub Release).
// Здесь — только проверка.
func EnsureInstalled() error {
	if _, err := os.Stat(BinaryPath()); err == nil {
		fmt.Println("[singbox] бинарник найден:", BinaryPath())
		return nil
	}
	// Если нет — пытаемся скачать из нашего релиза
	return downloadFromRelease()
}

// downloadFromRelease — скачивает бинарник AWG-сборки из нашего GitHub Release.
func downloadFromRelease() error {
	url := fmt.Sprintf(
		"https://github.com/vitosamin/singbox-manager/releases/download/sing-box-v%s/singbox-%s-aarch64-3.10",
		Version, Version,
	)
	fmt.Printf("[singbox] скачиваем: %s\n", url)

	if err := os.MkdirAll(InstallDir(), 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", InstallDir(), err)
	}

	tmpPath := InstallDir() + "/sing-box.new"
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("http.Get: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
	}

	out, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", tmpPath, err)
	}
	if _, err := out.ReadFrom(resp.Body); err != nil {
		out.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("download: %w", err)
	}
	out.Close()

	if err := os.Chmod(tmpPath, 0755); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}
	if err := os.Rename(tmpPath, BinaryPath()); err != nil {
		return fmt.Errorf("rename: %w", err)
	}

	fmt.Println("[singbox] бинарник установлен:", BinaryPath())
	return nil
}

type ringWriter struct{}

func (ringWriter) Write(p []byte) (int, error) {
	if logEnabled.Load() {
		LogRing.Write(p)
	}
	return len(p), nil
}

// Restart — перезапускает Sing-box с `-C config.d`.
// serverHosts — не используется в v3.0.0 (оставлен для совместимости API).
func Restart(serverHosts []string) error {
	_ = serverHosts // не используется в v3

	killOld()

	if _, err := os.Stat(BinaryPath()); err != nil {
		return fmt.Errorf("sing-box не установлен: %w", err)
	}
	if _, err := os.Stat(ConfigDir()); err != nil {
		return fmt.Errorf("config.d не найден: %w (сначала нажми «Применить»)", err)
	}

	LogRing.Clear()

	args := singBoxArgs("run", "-C", ConfigDir())
	fmt.Printf("[singbox] запускаем: %v\n", args)

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout = ringWriter{}
	cmd.Stderr = ringWriter{}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}

	pidPath := InstallDir() + "/sing-box.pid"
	pidStr := fmt.Sprintf("%d", cmd.Process.Pid)
	os.WriteFile(pidPath, []byte(pidStr), 0644)

	fmt.Printf("[singbox] запущен, PID=%d\n", cmd.Process.Pid)

	// Ждём Clash API
	deadline := time.Now().Add(10 * time.Second)
	client := &http.Client{Timeout: 1 * time.Second}
	apiOK := false
	for time.Now().Before(deadline) {
		resp, err := client.Get("http://127.0.0.1:9090/version")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				apiOK = true
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	if !apiOK {
		fmt.Println("[singbox] предупреждение: Clash API не поднялся")
		return nil
	}
	fmt.Println("[singbox] Clash API готов")
	return nil
}

// Stop — останавливает Sing-box.
func Stop() error {
	killOld()
	return nil
}

// killOld — убивает старый процесс Sing-box.
func killOld() {
	pidPath := InstallDir() + "/sing-box.pid"
	if data, err := os.ReadFile(pidPath); err == nil {
		var pid int
		fmt.Sscanf(string(data), "%d", &pid)
		if pid > 0 {
			if proc, err := os.FindProcess(pid); err == nil {
				proc.Kill()
				fmt.Printf("[singbox] остановлен старый процесс PID=%d\n", pid)
				time.Sleep(500 * time.Millisecond)
			}
		}
	}
	// Убиваем по имени (включая запущенные через ld-linux)
	exec.Command("killall", "sing-box").Run()
	exec.Command("sh", "-c",
		"ps -w | grep -E 'ld-linux.*sing-box' | grep -v grep | awk '{print $1}' | xargs -r kill -9").
		Run()
	time.Sleep(300 * time.Millisecond)
}

// WriteVersion — записывает версию в файл (для диагностики).
func WriteVersion() error {
	meta := fmt.Sprintf(`{"version":"%s","binary":"%s"}`, Version, BinaryPath())
	return os.WriteFile(filepath.Join(InstallDir(), "sing-box.meta.json"), []byte(meta), 0644)
}

// Hostname — для отладки.
func Hostname() string {
	h, _ := os.Hostname()
	return strings.TrimSpace(h)
}

// IsLinux — true если linux.
func IsLinux() bool {
	return runtime.GOOS == "linux"
}
