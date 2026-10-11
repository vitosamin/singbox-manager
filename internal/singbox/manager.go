package singbox

import (
	"fmt"
	"net"
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
	// Version — версия официального Sing-box (SagerNet).
	Version           = "1.14.2"
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

// ResolveServerIPs — превращает домены VLESS-серверов в IP-адреса.
// Нужно для исключения из iptables (иначе петля: сервер → proxy → сервер).
func ResolveServerIPs(servers []string) []string {
	var result []string
	seen := make(map[string]bool)

	for _, s := range servers {
		if s == "" {
			continue
		}

		if net.ParseIP(s) != nil {
			if !seen[s] {
				result = append(result, s)
				seen[s] = true
			}
			continue
		}

		ips, err := net.LookupHost(s)
		if err != nil {
			fmt.Printf("[singbox] предупреждение: не могу отрезолвить %s: %v\n", s, err)
			continue
		}
		for _, ip := range ips {
			if !seen[ip] {
				result = append(result, ip)
				seen[ip] = true
			}
		}
	}
	return result
}

// EnsureInstalled — проверяет, что бинарник Sing-box установлен.
// Если нет — скачивает официальный 1.14.2 от SagerNet.
func EnsureInstalled() error {
	if _, err := os.Stat(BinaryPath()); err == nil {
		fmt.Println("[singbox] бинарник найден:", BinaryPath())
		return nil
	}
	return downloadFromOfficial()
}

// downloadFromOfficial — скачивает официальный sing-box 1.14.2 от SagerNet.
func downloadFromOfficial() error {
	arch := "arm64"
	if runtime.GOARCH == "mips" || runtime.GOARCH == "mipsle" {
		arch = "mipsle"
	}
	url := fmt.Sprintf(
		"https://github.com/SagerNet/sing-box/releases/download/v%s/sing-box-%s-linux-%s.tar.gz",
		Version, Version, arch,
	)
	fmt.Printf("[singbox] скачиваем официальный: %s\n", url)

	if err := os.MkdirAll(InstallDir(), 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", InstallDir(), err)
	}

	tmpFile, err := os.CreateTemp("", "sing-box-*.tar.gz")
	if err != nil {
		return fmt.Errorf("CreateTemp: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("http.Get: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
	}

	if _, err := tmpFile.ReadFrom(resp.Body); err != nil {
		return fmt.Errorf("io.Copy: %w", err)
	}
	if _, err := tmpFile.Seek(0, 0); err != nil {
		return fmt.Errorf("Seek: %w", err)
	}

	if err := extractTarGz(tmpFile, InstallDir()); err != nil {
		return fmt.Errorf("extractTarGz: %w", err)
	}

	if _, err := os.Stat(BinaryPath()); err != nil {
		return fmt.Errorf("бинарник не появился: %w", err)
	}
	if err := os.Chmod(BinaryPath(), 0755); err != nil {
		return fmt.Errorf("chmod: %w", err)
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
func Restart(serverHosts []string) error {
	_ = serverHosts

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

	// Fix zombie: горутина ждёт завершения процесса и убирает зомби.
	go func() {
		_ = cmd.Wait()
	}()

	pidPath := InstallDir() + "/sing-box.pid"
	pidStr := fmt.Sprintf("%d", cmd.Process.Pid)
	os.WriteFile(pidPath, []byte(pidStr), 0644)

	fmt.Printf("[singbox] запущен, PID=%d\n", cmd.Process.Pid)

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
