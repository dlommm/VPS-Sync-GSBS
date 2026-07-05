package run

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dlommm/vps-sync-gsbs/internal/config"
	"github.com/dlommm/vps-sync-gsbs/internal/fetch"
	"github.com/dlommm/vps-sync-gsbs/internal/logx"
	"github.com/dlommm/vps-sync-gsbs/internal/pcgw"
	"github.com/dlommm/vps-sync-gsbs/internal/publish"
	"github.com/dlommm/vps-sync-gsbs/internal/r2"
	"github.com/dlommm/vps-sync-gsbs/internal/snapshot"
	"github.com/dlommm/vps-sync-gsbs/internal/validate"
)

func step(name string, fields map[string]interface{}, fn func() (map[string]interface{}, error)) error {
	logx.StepStart(name, fields)
	start := time.Now()
	extras, err := fn()
	if err != nil {
		logx.StepFail(name, time.Since(start), err)
		return fmt.Errorf("%s: %w", name, err)
	}
	logx.StepOK(name, time.Since(start), extras)
	return nil
}

// Outcome is the pipeline result: the publish result plus non-fatal warnings
// (e.g. a failed PCGW sync that did not block the publish).
type Outcome struct {
	publish.Result
	Warnings []string
}

// Weekly executes the full publisher pipeline: optional prod-DB refresh,
// optional PCGW sync, snapshot, full-bundle export, validation, R2 upload,
// DB snapshot backup, archive pruning. A PCGW sync failure is a warning, not
// an abort: publishing week-old data beats publishing nothing.
func Weekly(ctx context.Context, cfg config.Config) (Outcome, error) {
	runStart := time.Now()
	logx.RunStart("weekly", cfg.Summary())
	var res Outcome

	// Refresh from production first (when enabled) so the existence check below
	// sees the freshly fetched database rather than failing on a blank host.
	if cfg.FetchProdDB {
		if err := step("fetch_prod_db", map[string]interface{}{"src": config.RedactProdSrc(cfg.ProdDBSrc), "dest": cfg.GSBSDB}, func() (map[string]interface{}, error) {
			if err := fetch.ProdDB(cfg.ProdDBSrc, cfg.GSBSDB); err != nil {
				return nil, err
			}
			info, _ := os.Stat(cfg.GSBSDB)
			if info != nil {
				return map[string]interface{}{"bytes": info.Size()}, nil
			}
			return nil, nil
		}); err != nil {
			return res, err
		}
	}

	if err := step("check_database", map[string]interface{}{"db": cfg.GSBSDB}, func() (map[string]interface{}, error) {
		info, err := os.Stat(cfg.GSBSDB)
		if err != nil {
			return nil, fmt.Errorf("database not found at %s — run bootstrap first", cfg.GSBSDB)
		}
		if info.Size() == 0 {
			return nil, fmt.Errorf("database file is empty")
		}
		return map[string]interface{}{"bytes": info.Size()}, nil
	}); err != nil {
		return res, err
	}

	if cfg.RunPCGWSync {
		mode := "incremental"
		if cfg.PCGWSyncFull {
			mode = "full"
		}
		if err := step("pcgw_sync", map[string]interface{}{"mode": mode, "db": cfg.GSBSDB}, func() (map[string]interface{}, error) {
			n, err := pcgw.Sync(ctx, cfg.GSBSDB, cfg.PCGWSyncFull)
			if err != nil {
				return nil, err
			}
			return map[string]interface{}{"rows_upserted": n}, nil
		}); err != nil {
			// Non-fatal: publish with the data we already have rather than
			// skipping the weekly bundle. The warning reaches the webhook.
			res.Warnings = append(res.Warnings, fmt.Sprintf("pcgw sync failed (published existing data): %v", err))
		}
	} else {
		logx.StepStart("pcgw_sync", map[string]interface{}{"skipped": true})
		logx.StepOK("pcgw_sync", 0, map[string]interface{}{"skipped": true})
	}

	var snapPath string
	var cleanup func()

	if err := step("snapshot_db", map[string]interface{}{"db": cfg.GSBSDB}, func() (map[string]interface{}, error) {
		path, clean, err := snapshot.Backup(cfg.GSBSDB)
		if err != nil {
			return nil, err
		}
		snapPath = path
		cleanup = clean
		info, _ := os.Stat(path)
		if info != nil {
			return map[string]interface{}{"snapshot": path, "bytes": info.Size()}, nil
		}
		return map[string]interface{}{"snapshot": path}, nil
	}); err != nil {
		return res, err
	}
	defer cleanup()

	if err := step("export_bundle", map[string]interface{}{
		"out_dir": cfg.OutDir, "public_base": cfg.PublicBase,
	}, func() (map[string]interface{}, error) {
		var exportErr error
		res.Result, exportErr = publish.Export(ctx, snapPath, cfg.OutDir, cfg.PublicBase, cfg.GSBSVersion)
		if exportErr != nil {
			return nil, exportErr
		}
		files := artifactSizes(cfg.OutDir)
		return map[string]interface{}{
			"manifest_version": res.ManifestVersion,
			"files":            files,
		}, nil
	}); err != nil {
		return res, err
	}

	if err := step("validate_artifacts", map[string]interface{}{"out_dir": cfg.OutDir}, func() (map[string]interface{}, error) {
		if err := validate.Dir(cfg.OutDir); err != nil {
			return nil, err
		}
		return nil, nil
	}); err != nil {
		return res, err
	}

	if err := step("upload_r2", map[string]interface{}{
		"bucket": cfg.R2Bucket, "prefix": cfg.R2Prefix, "manifest_version": res.ManifestVersion,
	}, func() (map[string]interface{}, error) {
		client, err := r2.New(cfg)
		if err != nil {
			return nil, err
		}
		uploaded, err := client.UploadLive(ctx, cfg.OutDir, res.ManifestVersion)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"uploaded_keys": uploaded}, nil
	}); err != nil {
		return res, err
	}

	if cfg.DBBackup {
		if err := step("db_backup", map[string]interface{}{"keep": cfg.DBBackupKeep}, func() (map[string]interface{}, error) {
			client, err := r2.New(cfg)
			if err != nil {
				return nil, err
			}
			key, bytes, err := client.UploadDBBackup(ctx, snapPath)
			if err != nil {
				return nil, err
			}
			pruned, err := client.PruneDBBackups(ctx, cfg.DBBackupKeep)
			if err != nil {
				return map[string]interface{}{"key": key, "bytes": bytes, "prune_error": err.Error()}, nil
			}
			return map[string]interface{}{"key": key, "bytes": bytes, "pruned": pruned}, nil
		}); err != nil {
			// Non-fatal: a failed backup must not block the publish.
			res.Warnings = append(res.Warnings, fmt.Sprintf("db backup failed: %v", err))
		}
	}

	if cfg.R2Cleanup {
		if err := step("r2_cleanup", map[string]interface{}{"keep": cfg.R2Keep}, func() (map[string]interface{}, error) {
			client, err := r2.New(cfg)
			if err != nil {
				return nil, err
			}
			n, err := client.PruneArchives(ctx, cfg.R2Keep)
			if err != nil {
				return nil, err
			}
			return map[string]interface{}{"pruned": n}, nil
		}); err != nil {
			return res, err
		}
	}

	okFields := map[string]interface{}{
		"manifest_version": res.ManifestVersion,
		"public_base":      cfg.PublicBase,
	}
	if len(res.Warnings) > 0 {
		okFields["warnings"] = res.Warnings
	}
	logx.RunOK("weekly", time.Since(runStart), okFields)
	return res, nil
}

// Bootstrap fetches prod DB (if needed) and publishes the initial full bundle.
func Bootstrap(ctx context.Context, cfg config.Config) (Outcome, error) {
	logx.RunStart("bootstrap", cfg.Summary())

	if _, err := os.Stat(cfg.GSBSDB); err != nil {
		if cfg.ProdDBSrc == "" {
			return Outcome{}, fmt.Errorf("no DB at %s and PROD_DB_SRC not set", cfg.GSBSDB)
		}
		if err := step("fetch_prod_db", map[string]interface{}{"src": config.RedactProdSrc(cfg.ProdDBSrc)}, func() (map[string]interface{}, error) {
			if err := fetch.ProdDB(cfg.ProdDBSrc, cfg.GSBSDB); err != nil {
				return nil, err
			}
			return nil, nil
		}); err != nil {
			return Outcome{}, err
		}
	}
	return Weekly(ctx, cfg)
}

func artifactSizes(outDir string) map[string]int64 {
	out := map[string]int64{}
	for _, name := range []string{"index.json", "manifest.json.gz", "manifest.meta.json"} {
		info, err := os.Stat(filepath.Join(outDir, name))
		if err == nil {
			out[name] = info.Size()
		}
	}
	return out
}
