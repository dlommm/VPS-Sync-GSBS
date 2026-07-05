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
	"strings"

	"github.com/gsbs/gsbs/server/store"
)

var knownPlatforms = map[string]struct{}{
	"windows": {}, "linux": {}, "mac": {}, "macos": {}, "dos": {}, "": {},
}

// Dir validates manifest artifacts in a directory. Returns error on failure.
func Dir(dir string) error {
	var errs []string

	indexPath := filepath.Join(dir, "index.json")
	hasIndex := false
	if raw, err := os.ReadFile(indexPath); err == nil {
		hasIndex = true
		if e := validateIndex(raw, dir); e != "" {
			errs = append(errs, e)
		}
	}

	if _, err := os.Stat(filepath.Join(dir, "manifest.json.gz")); err == nil {
		if e := validateBundle(filepath.Join(dir, "manifest.json.gz"), "manifest.json.gz"); e != "" {
			errs = append(errs, e)
		}
	}
	if e := validateMeta(filepath.Join(dir, "manifest.meta.json"), filepath.Join(dir, "manifest.json.gz")); e != "" {
		errs = append(errs, e)
	}

	if !hasIndex && len(errs) == 0 {
		if _, err := os.Stat(filepath.Join(dir, "manifest.json.gz")); os.IsNotExist(err) {
			return nil // pre-launch
		}
		errs = append(errs, "manifest.json.gz present but index.json missing")
	}

	if len(errs) > 0 {
		return fmt.Errorf("validation failed:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

// validateIndex checks index.json exactly the way a GSBS server will read it:
// parse with GSBS's own store.ParsePCGWBundleIndex, then verify the advertised
// checksum matches the artifact on disk.
func validateIndex(raw []byte, dir string) string {
	idx, err := store.ParsePCGWBundleIndex(raw)
	if err != nil {
		return "index.json: " + err.Error()
	}
	if idx.Full.Version != idx.ManifestVersion {
		return fmt.Sprintf("index.json: full.version %d != manifest_version %d", idx.Full.Version, idx.ManifestVersion)
	}
	if idx.Full.SHA256 != "" {
		if got := fileSHA256(filepath.Join(dir, "manifest.json.gz")); got != "" && got != idx.Full.SHA256 {
			return fmt.Sprintf("index.json: full.sha256 mismatch (got %s)", got)
		}
	}
	if !strings.Contains(idx.Full.URL, "manifest.json.gz") {
		return fmt.Sprintf("index.json: full.url does not point at manifest.json.gz: %q", idx.Full.URL)
	}
	return ""
}

func validateBundle(path, label string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return label + ": read error: " + err.Error()
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return label + ": not valid gzip"
	}
	defer gz.Close()
	var bundle map[string]any
	if err := json.NewDecoder(gz).Decode(&bundle); err != nil {
		return label + ": not valid JSON: " + err.Error()
	}
	sv, _ := bundle["schema_version"].(float64)
	if int(sv) != 1 && int(sv) != 2 {
		return fmt.Sprintf("%s: invalid schema_version %v", label, bundle["schema_version"])
	}
	rows, _ := bundle["game_save_locations"].([]any)
	for i, r := range rows {
		row, _ := r.(map[string]any)
		if strings.TrimSpace(fmt.Sprint(row["game_id"])) == "" {
			return fmt.Sprintf("%s: game_save_locations[%d] missing game_id", label, i)
		}
		plat := strings.ToLower(fmt.Sprint(row["platform"]))
		if _, ok := knownPlatforms[plat]; !ok {
			return fmt.Sprintf("%s: game_save_locations[%d] unknown platform %q", label, i, plat)
		}
		tmpl := fmt.Sprint(row["path_template"])
		if strings.TrimSpace(tmpl) == "" {
			return fmt.Sprintf("%s: game_save_locations[%d] empty path_template", label, i)
		}
		for _, part := range strings.Split(strings.ReplaceAll(tmpl, "\\", "/"), "/") {
			if part == ".." {
				return fmt.Sprintf("%s: game_save_locations[%d] path traversal in %q", label, i, tmpl)
			}
		}
	}
	return ""
}

func validateMeta(metaPath, fullPath string) string {
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		return ""
	}
	var meta map[string]any
	if err := json.Unmarshal(raw, &meta); err != nil {
		return "manifest.meta.json: invalid JSON"
	}
	if meta["gsbs_version"] == "pending" {
		return ""
	}
	if sh, ok := meta["full_sha256"].(string); ok && sh != "" {
		if got := fileSHA256(fullPath); got != "" && got != sh {
			return fmt.Sprintf("manifest.meta.json: full_sha256 mismatch (got %s)", got)
		}
	}
	return ""
}

func fileSHA256(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
