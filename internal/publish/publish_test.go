package publish

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gsbs/gsbs/server/store"
)

func newTestDB(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "gsbs.db")
	st, err := store.NewSQLite(dbPath)
	if err != nil {
		t.Fatalf("create test db: %v", err)
	}
	st.Close()
	return dbPath
}

func TestExportProducesValidIndexAndIncrementsVersion(t *testing.T) {
	dbPath := newTestDB(t)
	outDir := t.TempDir()
	ctx := context.Background()

	res1, err := Export(ctx, dbPath, outDir, "https://example.com/manifest/", "test")
	if err != nil {
		t.Fatalf("first export: %v", err)
	}
	if res1.ManifestVersion != 1 {
		t.Fatalf("first publish should be v1, got %d", res1.ManifestVersion)
	}

	raw, err := os.ReadFile(filepath.Join(outDir, "index.json"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	// The produced index must round-trip through GSBS's own consumer parser.
	idx, err := store.ParsePCGWBundleIndex(raw)
	if err != nil {
		t.Fatalf("GSBS cannot parse produced index.json: %v", err)
	}
	if idx.Full.Version != idx.ManifestVersion {
		t.Fatalf("full.version %d != manifest_version %d", idx.Full.Version, idx.ManifestVersion)
	}
	if steps := store.PlanBundleCatchup(0, idx); len(steps) != 1 {
		t.Fatalf("fresh GSBS server should plan exactly 1 step, got %d", len(steps))
	}
	if steps := store.PlanBundleCatchup(idx.ManifestVersion, idx); len(steps) != 0 {
		t.Fatalf("current GSBS server should plan 0 steps, got %d", len(steps))
	}

	res2, err := Export(ctx, dbPath, outDir, "https://example.com/manifest/", "test")
	if err != nil {
		t.Fatalf("second export: %v", err)
	}
	if res2.ManifestVersion != 2 {
		t.Fatalf("second publish should be v2, got %d", res2.ManifestVersion)
	}

	if _, err := os.Stat(filepath.Join(outDir, "manifest.delta.json.gz")); !os.IsNotExist(err) {
		t.Fatal("delta artifact must not be produced")
	}
}

func TestPreviousIndexFallsBackToLiveURL(t *testing.T) {
	live := store.PCGWBundleIndex{
		ManifestVersion: 41,
		Full:            store.PCGWBundleIndexEntry{Version: 41, URL: "https://example.com/manifest/manifest.json.gz?h=abc"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/manifest/index.json" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(live)
	}))
	defer srv.Close()

	dbPath := newTestDB(t)
	outDir := t.TempDir() // no local index.json → must seed from the live URL

	res, err := Export(context.Background(), dbPath, outDir, srv.URL+"/manifest/", "test")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if res.ManifestVersion != 42 {
		t.Fatalf("expected version 42 (live 41 + 1), got %d — version regression would strand GSBS servers", res.ManifestVersion)
	}
}

func TestPreviousIndexMissingEverywhereStartsAtOne(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	dbPath := newTestDB(t)
	res, err := Export(context.Background(), dbPath, t.TempDir(), srv.URL+"/manifest/", "test")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if res.ManifestVersion != 1 {
		t.Fatalf("expected version 1 on true first publish, got %d", res.ManifestVersion)
	}
}

func TestUpdateManifestReleases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.releases.json")

	if err := updateManifestReleases(path, manifestReleaseEntry{Type: "full", ExportedAt: "2026-01-01T00:00:00Z", SHA256: "aaa"}); err != nil {
		t.Fatal(err)
	}
	if err := updateManifestReleases(path, manifestReleaseEntry{Type: "full", ExportedAt: "2026-01-08T00:00:00Z", SHA256: "bbb"}); err != nil {
		t.Fatal(err)
	}
	// Same timestamp+type replaces in place instead of duplicating.
	if err := updateManifestReleases(path, manifestReleaseEntry{Type: "full", ExportedAt: "2026-01-08T00:00:00Z", SHA256: "ccc"}); err != nil {
		t.Fatal(err)
	}

	raw, _ := os.ReadFile(path)
	var doc manifestReleases
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SchemaVersion != 1 || len(doc.Releases) != 2 {
		t.Fatalf("expected schema 1 with 2 releases, got %d with %d", doc.SchemaVersion, len(doc.Releases))
	}
	if doc.Releases[1].SHA256 != "ccc" {
		t.Fatalf("expected replacement sha ccc, got %s", doc.Releases[1].SHA256)
	}
}

// TestShrinkGuardRefusesCollapsedBundle: a bundle whose counts collapsed vs
// the previous publish must be refused before any artifact is overwritten.
func TestShrinkGuardRefusesCollapsedBundle(t *testing.T) {
	outDir := t.TempDir()
	prev := `{"schema_version":2,"counts":{"game_save_locations":19268,"games":54792,"game_data":15915,"catalog":54792}}`
	metaPath := filepath.Join(outDir, "manifest.meta.json")
	if err := os.WriteFile(metaPath, []byte(prev), 0o644); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(outDir, "manifest.json.gz")
	if err := os.WriteFile(sentinel, []byte("previous-good-bundle"), 0o644); err != nil {
		t.Fatal(err)
	}

	// An empty test DB exports ~zero counts — a total collapse vs prev meta.
	dbPath := newTestDB(t)
	_, err := Export(context.Background(), dbPath, outDir, "https://example.com/manifest/", "test")
	if err == nil || !strings.Contains(err.Error(), "shrink guard") {
		t.Fatalf("expected shrink guard refusal, got %v", err)
	}

	raw, _ := os.ReadFile(sentinel)
	if string(raw) != "previous-good-bundle" {
		t.Fatal("refused export must leave previous artifacts untouched")
	}

	// Deliberate override publishes.
	t.Setenv("FORCE_PUBLISH", "1")
	if _, err := Export(context.Background(), dbPath, outDir, "https://example.com/manifest/", "test"); err != nil {
		t.Fatalf("FORCE_PUBLISH=1 should publish: %v", err)
	}
}
