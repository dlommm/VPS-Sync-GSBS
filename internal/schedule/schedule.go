// Package schedule runs the publisher pipeline on a recurring schedule inside
// a long-lived process, so the container is the scheduler and a fresh host
// needs nothing but `docker compose up -d`.
package schedule

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/dlommm/vps-sync-gsbs/internal/config"
	"github.com/dlommm/vps-sync-gsbs/internal/notify"
	"github.com/dlommm/vps-sync-gsbs/internal/run"
	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog/log"
)

// Options controls the serve loop.
type Options struct {
	// Spec is a 5-field cron expression in Location's zone.
	Spec string
	// Location is the zone Spec is interpreted in.
	Location *time.Location
	// RunOnStart executes the pipeline once at startup instead of waiting for
	// the first scheduled tick.
	RunOnStart bool
	// AutoBootstrap seeds a missing database before the first run rather than
	// failing. See run.SeedDB.
	AutoBootstrap bool
}

// Serve runs the pipeline on Options.Spec until ctx is canceled.
//
// Runs cannot overlap: the loop below is the only caller of the pipeline and
// it runs each publish to completion before computing the next tick. A run that
// overruns its own schedule therefore delays the next one rather than stacking
// a second writer onto the same database.
func Serve(ctx context.Context, cfg config.Config, opts Options) error {
	if cfg.PublicBase == "" {
		return fmt.Errorf("PUBLIC_BASE required")
	}

	loc := opts.Location
	if loc == nil {
		loc = time.UTC
	}
	sched, err := cron.ParseStandard(opts.Spec)
	if err != nil {
		return fmt.Errorf("invalid SCHEDULE %q: %w", opts.Spec, err)
	}

	// Seed the database before the first run so a rebuilt host converges on its
	// own instead of waiting for an operator to run bootstrap by hand.
	if opts.AutoBootstrap {
		if _, statErr := os.Stat(cfg.GSBSDB); statErr != nil {
			log.Info().Str("db", cfg.GSBSDB).Msg("serve: no database present — seeding")
			if err := run.SeedDB(ctx, cfg); err != nil {
				return fmt.Errorf("auto-bootstrap: %w", err)
			}
		}
	}

	execute := func(reason string) {
		start := time.Now()
		res, runErr := run.Weekly(ctx, cfg)

		// A shutdown that interrupts a publish is not a failure. Reporting it
		// as one would page on every `docker stop` that happened to land
		// mid-run, and the next start simply resumes.
		if runErr != nil && ctx.Err() != nil {
			log.Info().Dur("took", time.Since(start)).Msg("serve: run interrupted by shutdown")
			return
		}

		notify.RunResult(cfg.WebhookURL, "serve:"+reason, time.Since(start), res.ManifestVersion, res.Warnings, runErr)
		if runErr != nil {
			// A failed run must not kill the process: the next tick is the
			// retry, and a publisher that exits on a transient upstream error
			// stops publishing entirely until someone notices.
			log.Error().Err(runErr).Dur("took", time.Since(start)).Msg("serve: run failed — will retry on the next tick")
			return
		}
		log.Info().
			Int("manifest_version", res.ManifestVersion).
			Dur("took", time.Since(start)).
			Time("next_run", sched.Next(time.Now().In(loc))).
			Msg("serve: run complete")
	}

	log.Info().
		Str("schedule", opts.Spec).
		Str("tz", loc.String()).
		Bool("run_on_start", opts.RunOnStart).
		Time("next_run", sched.Next(time.Now().In(loc))).
		Msg("serve: scheduler started")

	if opts.RunOnStart {
		execute("startup")
	}

	for {
		now := time.Now().In(loc)
		next := sched.Next(now)
		timer := time.NewTimer(next.Sub(now))
		select {
		case <-ctx.Done():
			timer.Stop()
			log.Info().Msg("serve: shutting down")
			return nil
		case <-timer.C:
			execute("scheduled")
		}
	}
}
