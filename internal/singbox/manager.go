package singbox

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
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
	Version           = "1.14.0"
	DefaultInstallDir = "/opt/etc/sing-box"
)

// LogRing — глобальный буфер логов в оперативке.
var LogRing = logger.NewRing(2000)

// logEnabled — атомарный флаг: писать ли логи в Ring.
var logEnabled atomic.Bool

func init() {
	logEnabled.Store(true)
}

// SetLogEnabled включает/выключает логирование в Ring.
func SetLogEnabled(enabled bool) {
	logEnabled.Store(enabled)
}

// SetLogRingSize меняет размер буфера.
func SetLogRingSize(size int) {
	LogRing = logger.NewRing(size)
}

func InstallDir() string {
	if v := os.Getenv("SINGBOX_DIR"); v != "" {
		return v
	}
	return DefaultInstallDir
}

func BinaryPath() string { return InstallDir() + "/sing-box" }
func ConfigPath() string { return InstallDir() + "/config.json" }

func EnsureInstalled() error {
	if _, err := os.Stat(BinaryPath()); err == nil {
		fmt.Println("[singbox] уже установлен:", BinaryPath())
		return nil
	}
	arch := "arm64"
	if runtime.GOARCH == "mips" || runtime.GOARCH == "mipsle" {
		arch = "mipsle"
	}
	url := fmt.Sprintf(
		"https://github.com/SagerNet/sing-box/releases/download/v%s/sing-box-%s-linux-%s.tar.gz",
		Version, Version, arch,
	)
	fmt.Printf("[singbox] скачиваем для linux/%s: %s\n", arch, url)
	if err := os.MkdirAll(InstallDir(), 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", InstallDir(), err)
	}
	tmpFile, err := os.CreateTemp("", "sing-box-*.tar.gz")
	if err != nil {
		return fmt.Errorf("CreateTemp: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("http.Get: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
	}
	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		return fmt.Errorf("io.Copy: %w", err)
	}
	if _, err := tmpFile.Seek(0, io.SeekStart); err != nil {
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
	fmt.Println("[singbox] установлен:", BinaryPath())
	return nil
}

func extractTarGz(r io.Reader, destDir string) error {
	gzr, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := header.Name
		if idx := strings.IndexByte(name, '/'); idx != -1 {
			name = name[idx+1:]
		}
		if name == "" {
			continue
		}
		destPath := filepath.Join(destDir, name)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(destPath, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
				return err
			}
			out, err := os.Create(destPath)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
			if err := os.Chmod(destPath, os.FileMode(header.Mode)); err != nil {
				return err
			}
		}
	}
	return nil
}

// writer-обёртка, которая пишет в Ring только если логирование включено.
type ringWriter struct{}

func (ringWriter) Write(p []byte) (int, error) {
	if logEnabled.Load() {
		LogRing.Write(p)
	}
	return len(p), nil
}

// Restart перезапускает sing-box. Логи — только в RAM.
func Restart() error {
	killOld()
	if _, err := os.Stat(BinaryPath()); err != nil {
		return fmt.Errorf("sing-box не установлен: %w", err)
	}
	if _, err := os.Stat(ConfigPath()); err != nil {
		return fmt.Errorf("config.json не найден: %w", err)
	}

	LogRing.Clear()

	fmt.Println("[singbox] запускаем:", BinaryPath())
	cmd := exec.Command(BinaryPath(), "run", "-c", ConfigPath())

	// Пишем и stdout, и stderr в Ring (или выбрасываем)
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
	for time.Now().Before(deadline) {
		resp, err := client.Get("http://127.0.0.1:9090/version")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				fmt.Println("[singbox] Clash API готов")
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	fmt.Println("[singbox] предупреждение: Clash API не поднялся")
	return nil
}

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
	time.Sleep(300 * time.Millisecond)
}
