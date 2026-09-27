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

	// Scheduler settings, used by `vps-sync serve` (the always-on container).
	Schedule      string // 5-field cron expression
	ScheduleTZ    string // IANA zone Schedule is read in
	RunOnStart    bool   // publish once at startup instead of waiting for the first tick
	AutoBootstrap bool   // seed a missing DB (PROD_DB_SRC, else newest R2 backup) before the first run

	R2AccessKey string
	R2SecretKey string
	R2Endpoint  string
	R2Bucket    string
	R2Prefix    string // object prefix, default manifest
	R2Cleanup   bool
	R2Keep      int

	DBBackup     bool
	DBBackupKeep int

	GSBSVersion string
	WebhookURL  string

	// PCGWBotLogin records whether a PCGW bot password is configured. GSBS
	// reads the credentials from the environment itself; this is only for the
	// run summary, so the password never passes through here.
	PCGWBotLogin bool

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
		Schedule:        env("SCHEDULE", "0 3 * * 0"), // Sunday 03:00
		ScheduleTZ:      env("SCHEDULE_TZ", "UTC"),
		RunOnStart:      envBool("RUN_ON_START", false),
		AutoBootstrap:   envBool("AUTO_BOOTSTRAP", true),
		R2AccessKey:     os.Getenv("AWS_ACCESS_KEY_ID"),
		R2SecretKey:     os.Getenv("AWS_SECRET_ACCESS_KEY"),
		R2Endpoint:      strings.TrimSpace(os.Getenv("R2_ENDPOINT")),
		R2Bucket:        strings.TrimSpace(os.Getenv("R2_BUCKET")),
		R2Prefix:        env("R2_PREFIX", "manifest"),
		R2Cleanup:       envBool("R2_CLEANUP", true),
		R2Keep:          envInt("R2_KEEP", 24),
		DBBackup:        envBool("DB_BACKUP", true),
		DBBackupKeep:    envInt("DB_BACKUP_KEEP", 6),
		GSBSVersion:     env("GSBS_VERSION", "vps-sync"),
		WebhookURL:      strings.TrimSpace(os.Getenv("WEBHOOK_URL")),
		PCGWBotLogin:    strings.TrimSpace(os.Getenv("GSBS_PCGW_BOT_USER")) != "" && os.Getenv("GSBS_PCGW_BOT_PASSWORD") != "",
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
