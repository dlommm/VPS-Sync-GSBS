package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/dlommm/vps-sync-gsbs/internal/config"
	"github.com/dlommm/vps-sync-gsbs/internal/fetch"
	"github.com/dlommm/vps-sync-gsbs/internal/logx"
	"github.com/dlommm/vps-sync-gsbs/internal/notify"
	"github.com/dlommm/vps-sync-gsbs/internal/pcgw"
	"github.com/dlommm/vps-sync-gsbs/internal/publish"
	"github.com/dlommm/vps-sync-gsbs/internal/repair"
	"github.com/dlommm/vps-sync-gsbs/internal/run"
	"github.com/dlommm/vps-sync-gsbs/internal/snapshot"
	"github.com/dlommm/vps-sync-gsbs/internal/validate"
)

func main() {
	os.Exit(runMain())
}

func runMain() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		return 1
	}

	if err := logx.Setup(logx.Options{
		File:         cfg.LogFile,
		MirrorStderr: cfg.LogMirrorStderr,
		Level:        cfg.LogLevel,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "log setup error: %v\n", err)
		return 1
	}
	defer logx.Close()

	if len(os.Args) < 2 {
		usage()
		return 1
	}

	ctx := context.Background()
	cmd := os.Args[1]
	start := time.Now()
	var runErr error
	manifestVersion := 0

	switch cmd {
	case "run", "weekly":
		if cfg.PublicBase == "" {
			runErr = fmt.Errorf("PUBLIC_BASE required")
		} else {
			var res run.Outcome
			res, runErr = run.Weekly(ctx, cfg)
			manifestVersion = res.ManifestVersion
			notify.RunResult(cfg.WebhookURL, cmd, time.Since(start), manifestVersion, res.Warnings, runErr)
			break
		}
		notify.RunResult(cfg.WebhookURL, cmd, time.Since(start), manifestVersion, nil, runErr)
	case "bootstrap":
		if cfg.PublicBase == "" {
			runErr = fmt.Errorf("PUBLIC_BASE required")
		} else {
			var res run.Outcome
			res, runErr = run.Bootstrap(ctx, cfg)
			manifestVersion = res.ManifestVersion
			notify.RunResult(cfg.WebhookURL, cmd, time.Since(start), manifestVersion, res.Warnings, runErr)
			break
		}
		notify.RunResult(cfg.WebhookURL, cmd, time.Since(start), manifestVersion, nil, runErr)
	case "fetch-prod":
		logx.RunStart("fetch-prod", cfg.Summary())
		runErr = fetch.ProdDB(cfg.ProdDBSrc, cfg.GSBSDB)
		if runErr == nil {
			logx.RunOK("fetch-prod", time.Since(start), map[string]interface{}{"db": cfg.GSBSDB})
		}
	case "pcgw-sync":
		logx.RunStart("pcgw-sync", cfg.Summary())
		var n int
		n, runErr = pcgw.SyncWithTimeout(cfg.GSBSDB, cfg.PCGWSyncFull)
		if runErr == nil {
			logx.RunOK("pcgw-sync", time.Since(start), map[string]interface{}{"rows_upserted": n})
		}
	case "export":
		if cfg.PublicBase == "" {
			runErr = fmt.Errorf("PUBLIC_BASE required")
			break
		}
		logx.RunStart("export", cfg.Summary())
		snap, cleanup, err := snapshot.Backup(cfg.GSBSDB)
		if err != nil {
			runErr = err
			break
		}
		defer cleanup()
		var res publish.Result
		res, runErr = publish.Export(ctx, snap, cfg.OutDir, cfg.PublicBase, cfg.GSBSVersion)
		if runErr == nil {
			runErr = validate.Dir(cfg.OutDir)
		}
		if runErr == nil {
			logx.RunOK("export", time.Since(start), map[string]interface{}{
				"manifest_version": res.ManifestVersion,
				"out_dir":          cfg.OutDir,
			})
		}
	case "validate":
		logx.RunStart("validate", map[string]interface{}{"out_dir": cfg.OutDir})
		runErr = validate.Dir(cfg.OutDir)
		if runErr == nil {
			logx.RunOK("validate", time.Since(start), nil)
		}
	case "repair-db":
		logx.RunStart("repair-db", map[string]interface{}{"db": cfg.GSBSDB})
		var created []string
		created, runErr = repair.DB(cfg.GSBSDB)
		if runErr == nil {
			logx.RunOK("repair-db", time.Since(start), map[string]interface{}{"created": created})
		}
	default:
		usage()
		return 1
	}

	if runErr != nil {
		logx.RunFail(cmd, time.Since(start), runErr)
		if repair.IsSchemaError(runErr) {
			fmt.Fprintln(os.Stderr, "hint: this database is missing GSBS tables (sanitized seed?) — run: vps-sync repair-db")
		}
		return 1
	}
	return 0
}

func usage() {
	fmt.Fprintf(os.Stderr, `vps-sync — GSBS PCGW manifest publisher (self-contained)

Usage:
  vps-sync bootstrap     First run: fetch prod DB + publish full bundle to R2
  vps-sync run           Weekly job: PCGW sync + export + R2 upload
  vps-sync fetch-prod    Pull gsbs.db from PROD_DB_SRC via rsync
  vps-sync pcgw-sync     Incremental PCGW API sync into local DB
  vps-sync export        Export full bundle locally (no upload)
  vps-sync validate      Validate artifacts in OUT_DIR
  vps-sync repair-db     Recreate GSBS tables missing from a sanitized seed DB

Every publish is a full bundle: GSBS servers merge it from any prior version,
so index.json only ever advertises the current full manifest.

Logs: LOG_FILE (default <OUT_DIR>/../logs/vps-sync.log). See deploy/logrotate.gsbs-vps-sync.
Configure via .env or environment (see .env.example).
`)
}
