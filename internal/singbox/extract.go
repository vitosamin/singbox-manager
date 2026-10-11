package singbox

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// extractTarGz распаковывает tar.gz в destDir.
func extractTarGz(r io.Reader, destDir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar.Next: %w", err)
		}

		// Берём только basename (SagerNet кладёт бинарник в подкаталог)
		name := filepath.Base(hdr.Name)
		if name == "." || name == ".." || name == "" {
			continue
		}
		// Нас интересует только sing-box
		if name != "sing-box" {
			continue
		}

		outPath := filepath.Join(destDir, name)
		out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
		if err != nil {
			return fmt.Errorf("create %s: %w", outPath, err)
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return fmt.Errorf("copy %s: %w", outPath, err)
		}
		out.Close()
		fmt.Printf("[singbox] распакован: %s\n", outPath)
	}
	return nil
}

// stripPrefix удаляет префикс из пути.
func stripPrefix(path, prefix string) string {
	return strings.TrimPrefix(path, prefix)
}
