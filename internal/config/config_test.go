package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnvFileAndDotEnvBothLoad(t *testing.T) {
	// The documented split-secrets flow: AWS_* in ENV_FILE, everything else in
	// .env. Both must load, with ENV_FILE winning on overlap.
	dir := t.TempDir()
	secrets := filepath.Join(dir, "secrets.env")
	if err := os.WriteFile(secrets, []byte("AWS_ACCESS_KEY_ID=from-secrets\nR2_BUCKET=secret-bucket\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("R2_BUCKET=dotenv-bucket\nR2_PREFIX=custom\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("ENV_FILE", secrets)
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("R2_BUCKET", "")
	t.Setenv("R2_PREFIX", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.R2AccessKey != "from-secrets" {
		t.Fatalf("ENV_FILE secrets not loaded, got %q", cfg.R2AccessKey)
	}
	if cfg.R2Bucket != "secret-bucket" {
		t.Fatalf("ENV_FILE should win overlap, got %q", cfg.R2Bucket)
	}
	if cfg.R2Prefix != "custom" {
		t.Fatalf(".env settings lost when ENV_FILE is set, got %q", cfg.R2Prefix)
	}
}

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	for _, k := range []string{"ENV_FILE", "GSBS_DB", "OUT_DIR", "RUN_PCGW_SYNC", "R2_PREFIX", "R2_KEEP", "LOG_FILE"} {
		t.Setenv(k, "")
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GSBSDB != filepath.Join(dir, "data/gsbs.db") {
		t.Fatalf("unexpected default GSBS_DB: %s", cfg.GSBSDB)
	}
	if !cfg.RunPCGWSync {
		t.Fatal("RUN_PCGW_SYNC should default to true")
	}
	if cfg.R2Keep != 24 {
		t.Fatalf("R2_KEEP should default to 24, got %d", cfg.R2Keep)
	}
	if cfg.LogFile != filepath.Join(dir, "logs", "vps-sync.log") {
		t.Fatalf("unexpected default log file: %s", cfg.LogFile)
	}
}

func TestRedactSrc(t *testing.T) {
	cases := map[string]string{
		"":                         "",
		"user@host:/srv/gsbs.db":   "user@host:***",
		"plain-path-without-colon": "***",
	}
	for in, want := range cases {
		if got := redactSrc(in); got != want {
			t.Fatalf("redactSrc(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEnvBool(t *testing.T) {
	t.Setenv("X_BOOL", "yes")
	if !envBool("X_BOOL", false) {
		t.Fatal("yes should be true")
	}
	t.Setenv("X_BOOL", "off")
	if envBool("X_BOOL", true) {
		t.Fatal("off should be false")
	}
	t.Setenv("X_BOOL", "garbage")
	if !envBool("X_BOOL", true) {
		t.Fatal("garbage should fall back to default")
	}
}
