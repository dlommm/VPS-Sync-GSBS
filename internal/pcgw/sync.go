package pcgw

import (
	"context"
	"fmt"
	"time"

	"github.com/gsbs/gsbs/pkg/pcgw"
	"github.com/gsbs/gsbs/server/job"
	"github.com/gsbs/gsbs/server/store"
)

// Sync runs incremental or full PCGW API sync into dbPath.
func Sync(ctx context.Context, dbPath string, full bool) (int, error) {
	st, err := store.NewSQLite(dbPath)
	if err != nil {
		return 0, err
	}
	defer st.Close()

	mode := "incremental"
	if full {
		mode = "full"
	}
	runID, err := st.StartPCGWSyncRun(ctx, mode)
	if err != nil {
		return 0, err
	}

	client := pcgw.NewClient()
	n, err := job.PCGWSync(ctx, st, client, nil, job.PCGWSyncOptions{
		Full:      full,
		SyncRunID: runID,
	})
	if err != nil {
		return 0, fmt.Errorf("pcgw sync: %w", err)
	}
	return n, nil
}

// SyncWithTimeout wraps Sync with a 24h deadline.
func SyncWithTimeout(dbPath string, full bool) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
	defer cancel()
	return Sync(ctx, dbPath, full)
}
