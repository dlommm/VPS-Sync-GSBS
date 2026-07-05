package validate

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func writeBundle(t *testing.T, dir string, rows []map[string]any) (sha string, size int) {
	t.Helper()
	payload := map[string]any{
		"schema_version":      2,
		"game_save_locations": rows,
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if err := json.NewEncoder(gz).Encode(payload); err != nil {
		t.Fatal(err)
	}
	gz.Close()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json.gz"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:]), buf.Len()
}

func writeIndex(t *testing.T, dir, sha string, size int) {
	t.Helper()
	idx := fmt.Sprintf(`{
  "manifest_version": 3,
  "generated_at": "2026-07-04T00:00:00Z",
  "full": {"version": 3, "url": "https://example.com/manifest/manifest.json.gz?h=%s", "sha256": "%s", "bytes": %d}
}`, sha[:16], sha, size)
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte(idx), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDirAcceptsValidArtifacts(t *testing.T) {
	dir := t.TempDir()
	sha, size := writeBundle(t, dir, []map[string]any{
		{"game_id": "g1", "platform": "windows", "path_template": "%APPDATA%/Game/Saves"},
	})
	writeIndex(t, dir, sha, size)

	if err := Dir(dir); err != nil {
		t.Fatalf("valid artifacts rejected: %v", err)
	}
}

func TestDirRejectsShaMismatch(t *testing.T) {
	dir := t.TempDir()
	_, size := writeBundle(t, dir, nil)
	bogus := "0000000000000000000000000000000000000000000000000000000000000000"
	writeIndex(t, dir, bogus, size)

	if err := Dir(dir); err == nil {
		t.Fatal("sha mismatch not detected")
	}
}

func TestDirRejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	sha, size := writeBundle(t, dir, []map[string]any{
		{"game_id": "g1", "platform": "windows", "path_template": "saves/../../etc/passwd"},
	})
	writeIndex(t, dir, sha, size)

	if err := Dir(dir); err == nil {
		t.Fatal("path traversal not detected")
	}
}

func TestDirRejectsBundleWithoutIndex(t *testing.T) {
	dir := t.TempDir()
	writeBundle(t, dir, nil)

	if err := Dir(dir); err == nil {
		t.Fatal("bundle without index.json must fail validation")
	}
}

func TestDirAcceptsEmptyPreLaunchDir(t *testing.T) {
	if err := Dir(t.TempDir()); err != nil {
		t.Fatalf("empty dir should validate (pre-launch): %v", err)
	}
}
