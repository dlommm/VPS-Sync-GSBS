package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config holds VPS publisher settings (env + optional .env file).
type Config struct {
	GSBSDB     string
	OutDir     string
	PublicBase string

	ProdDBSrc   string
	FetchProdDB bool

	RunPCGWSync  bool
	PCGWSyncFull bool

	R2AccessKey string
	R2SecretKey string
	R2Endpoint  string
	R2Bucket    string
	R2Prefix    string // object prefix, default manifest
	R2Cleanup   bool
	R2Keep      int

	GSBSVersion string
	WebhookURL  string

	LogFile         string
	LogLevel        string
	LogMirrorStderr bool
}

func Load() (Config, error) {
	// ENV_FILE (secrets) loads first and wins; .env still supplies everything
	// else so a split-secrets setup doesn't lose the non-secret settings.
	if p := os.Getenv("ENV_FILE"); p != "" {
		loadDotEnv(p)
	}
	if _, err := os.Stat(".env"); err == nil {
		loadDotEnv(".env")
	}

	cfg := Config{
		GSBSDB:          env("GSBS_DB", "data/gsbs.db"),
		OutDir:          env("OUT_DIR", "out"),
		PublicBase:      strings.TrimSpace(os.Getenv("PUBLIC_BASE")),
		ProdDBSrc:       strings.TrimSpace(os.Getenv("PROD_DB_SRC")),
		FetchProdDB:     envBool("FETCH_PROD_DB", false),
		RunPCGWSync:     envBool("RUN_PCGW_SYNC", true),
		PCGWSyncFull:    envBool("PCGW_SYNC_FULL", false),
		R2AccessKey:     os.Getenv("AWS_ACCESS_KEY_ID"),
		R2SecretKey:     os.Getenv("AWS_SECRET_ACCESS_KEY"),
		R2Endpoint:      strings.TrimSpace(os.Getenv("R2_ENDPOINT")),
		R2Bucket:        strings.TrimSpace(os.Getenv("R2_BUCKET")),
		R2Prefix:        env("R2_PREFIX", "manifest"),
		R2Cleanup:       envBool("R2_CLEANUP", true),
		R2Keep:          envInt("R2_KEEP", 24),
		GSBSVersion:     env("GSBS_VERSION", "vps-sync"),
		WebhookURL:      strings.TrimSpace(os.Getenv("WEBHOOK_URL")),
		LogFile:         env("LOG_FILE", ""), // default resolved after OUT_DIR
		LogLevel:        env("LOG_LEVEL", "info"),
		LogMirrorStderr: envBool("LOG_MIRROR_STDERR", false),
	}

	if !filepath.IsAbs(cfg.GSBSDB) {
		wd, _ := os.Getwd()
		cfg.GSBSDB = filepath.Join(wd, cfg.GSBSDB)
	}
	if !filepath.IsAbs(cfg.OutDir) {
		wd, _ := os.Getwd()
		cfg.OutDir = filepath.Join(wd, cfg.OutDir)
	}
	if cfg.LogFile == "" {
		cfg.LogFile = filepath.Join(filepath.Dir(cfg.OutDir), "logs", "vps-sync.log")
	} else if !filepath.IsAbs(cfg.LogFile) {
		wd, _ := os.Getwd()
		cfg.LogFile = filepath.Join(wd, cfg.LogFile)
	}
	return cfg, nil
}

func (c Config) R2Configured() bool {
	return c.R2AccessKey != "" && c.R2SecretKey != "" && c.R2Endpoint != "" && c.R2Bucket != ""
}

func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func envBool(k string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

func envInt(k string, def int) int {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
