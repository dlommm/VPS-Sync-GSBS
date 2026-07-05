package fetch

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ProdDB pulls gsbs.db from a remote rsync source into destPath.
func ProdDB(src, destPath string) error {
	if strings.TrimSpace(src) == "" {
		return fmt.Errorf("PROD_DB_SRC not set")
	}
	if _, err := exec.LookPath("rsync"); err != nil {
		return fmt.Errorf("rsync required")
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		return fmt.Errorf("sqlite3 required")
	}

	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, "gsbs-prod-*.db")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	tmp.Close()

	cmd := exec.Command("rsync", "-avz", src, tmpPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rsync: %w: %s", err, strings.TrimSpace(string(out)))
	}

	check := exec.Command("sqlite3", tmpPath, "PRAGMA quick_check;")
	out, err := check.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ok") {
		os.Remove(tmpPath)
		return fmt.Errorf("downloaded DB failed integrity check")
	}

	if _, err := os.Stat(destPath); err == nil {
		backup := destPath + ".bak." + time.Now().UTC().Format("20060102-150405")
		if err := copyFile(destPath, backup); err != nil {
			return err
		}
		os.Remove(destPath + "-wal")
		os.Remove(destPath + "-shm")
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		return err
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = out.ReadFrom(in)
	return err
}
