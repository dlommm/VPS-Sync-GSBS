package publish

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gsbs/gsbs/server/store"
	"github.com/rs/zerolog/log"
)

// Result holds exported artifact paths and metadata.
type Result struct {
	ManifestVersion int
	IndexPath       string
	OutDir          string
}

// Export writes the full manifest bundle, manifest.meta.json, and index.json
// into outDir. Publishing is full-bundle-only: GSBS servers merge the complete
// bundle from any prior version, so every publish overwrites the same artifact
// and bumps manifest_version by 1 (index schema and versioning reuse GSBS's own
// store.AdvanceBundleIndex, so consumer and publisher can never drift).
func Export(ctx context.Context, dbPath, outDir, publicBase, gsbsVersion string) (Result, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return Result{}, err
	}
	publicBase = strings.TrimSpace(publicBase)
	if publicBase == "" {
		return Result{}, fmt.Errorf("PUBLIC_BASE required for index.json")
	}

	st, err := store.NewSQLite(dbPath)
	if err != nil {
		return Result{}, err
	}
	defer st.Close()

	data, meta, err := st.ExportPCGWManifestBundleWithOpts(ctx, gsbsVersion, store.PCGWBundleExportOpts{Lite: true})
	if err != nil {
		return Result{}, err
	}

	gzPath := filepath.Join(outDir, "manifest.json.gz")
	if err := os.WriteFile(gzPath, data, 0o644); err != nil {
		return Result{}, err
	}

	metaPath := filepath.Join(outDir, "manifest.meta.json")
	rawMeta, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(metaPath, rawMeta, 0o644); err != nil {
		return Result{}, err
	}

	releasesPath := filepath.Join(outDir, "manifest.releases.json")
	_ = updateManifestReleases(releasesPath, manifestReleaseEntry{
		Type:           "full",
		ExportedAt:     meta.ExportedAt,
		FullExportedAt: meta.FullExportedAt,
		SHA256:         meta.FullSHA256,
	})

	// Deltas are no longer published; drop any stale local artifact so it can't
	// be uploaded again.
	_ = os.Remove(filepath.Join(outDir, "manifest.delta.json.gz"))

	indexPath := filepath.Join(outDir, "index.json")
	prevIndex := previousIndex(ctx, indexPath, publicBase)

	nextIndex, err := store.AdvanceBundleIndex(prevIndex, meta.FullSHA256, len(data), publicBase, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return Result{}, fmt.Errorf("advance index: %w", err)
	}
	rawIndex, _ := json.MarshalIndent(nextIndex, "", "  ")
	if err := os.WriteFile(indexPath, rawIndex, 0o644); err != nil {
		return Result{}, err
	}

	return Result{
		ManifestVersion: nextIndex.ManifestVersion,
		IndexPath:       indexPath,
		OutDir:          outDir,
	}, nil
}

// previousIndex loads the last published index so manifest_version keeps
// increasing monotonically. It prefers the local copy in outDir; when that is
// absent (fresh checkout or wiped OUT_DIR) it falls back to the live published
// index at publicBase. Without this fallback a redeploy would restart
// numbering at 1 and every GSBS server already past that version would treat
// itself as current and stop updating.
func previousIndex(ctx context.Context, indexPath, publicBase string) store.PCGWBundleIndex {
	if raw, err := os.ReadFile(indexPath); err == nil {
		if parsed, perr := store.ParsePCGWBundleIndex(raw); perr == nil {
			return parsed
		}
	}

	if !strings.HasSuffix(publicBase, "/") {
		publicBase += "/"
	}
	url := publicBase + "index.json"
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return store.PCGWBundleIndex{}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Warn().Str("url", url).Err(err).Msg("live index fetch failed; starting from version 0")
		return store.PCGWBundleIndex{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return store.PCGWBundleIndex{}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return store.PCGWBundleIndex{}
	}
	parsed, err := store.ParsePCGWBundleIndex(raw)
	if err != nil {
		log.Warn().Str("url", url).Err(err).Msg("live index unparsable; starting from version 0")
		return store.PCGWBundleIndex{}
	}
	log.Info().Int("manifest_version", parsed.ManifestVersion).Str("url", url).Msg("seeded previous index from live published copy")
	return parsed
}
