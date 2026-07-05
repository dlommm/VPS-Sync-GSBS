package publish

import (
	"encoding/json"
	"os"
	"strings"
)

type manifestReleaseEntry struct {
	Type           string `json:"type"`
	ExportedAt     string `json:"exported_at"`
	FullExportedAt string `json:"full_exported_at,omitempty"`
	SHA256         string `json:"sha256"`
}

type manifestReleases struct {
	SchemaVersion int                    `json:"schema_version"`
	Releases      []manifestReleaseEntry `json:"releases"`
}

func updateManifestReleases(path string, entry manifestReleaseEntry) error {
	var doc manifestReleases
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &doc)
	}
	if doc.SchemaVersion == 0 {
		doc.SchemaVersion = 1
	}
	entry.Type = strings.TrimSpace(entry.Type)
	entry.ExportedAt = strings.TrimSpace(entry.ExportedAt)
	entry.FullExportedAt = strings.TrimSpace(entry.FullExportedAt)
	entry.SHA256 = strings.TrimSpace(entry.SHA256)

	replaced := false
	for i := range doc.Releases {
		if doc.Releases[i].ExportedAt == entry.ExportedAt && doc.Releases[i].Type == entry.Type {
			doc.Releases[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		doc.Releases = append(doc.Releases, entry)
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}
