package snapshot

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Backup creates a consistent online SQLite backup of src into a temp file.
func Backup(src string) (string, func(), error) {
	if err := requireSQLite3(); err != nil {
		return "", nil, err
	}
	dir := filepath.Dir(src)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", nil, err
	}
	tmp, err := os.CreateTemp(dir, "gsbs-snap-*.db")
	if err != nil {
		return "", nil, err
	}
	path := tmp.Name()
	tmp.Close()

	cmd := exec.Command("sqlite3", src, fmt.Sprintf(".backup '%s'", path))
	if out, err := cmd.CombinedOutput(); err != nil {
		os.Remove(path)
		return "", nil, fmt.Errorf("sqlite backup: %w: %s", err, strings.TrimSpace(string(out)))
	}

	check := exec.Command("sqlite3", path, "PRAGMA quick_check;")
	out, err := check.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ok") {
		os.Remove(path)
		return "", nil, fmt.Errorf("snapshot integrity check failed: %s", strings.TrimSpace(string(out)))
	}

	cleanup := func() {
		os.Remove(path)
		os.Remove(path + "-wal")
		os.Remove(path + "-shm")
	}
	return path, cleanup, nil
}

func requireSQLite3() error {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		return fmt.Errorf("sqlite3 CLI required for online snapshots")
	}
	return nil
}
