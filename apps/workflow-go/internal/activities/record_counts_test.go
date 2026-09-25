package activities

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/dataflow-poc/workflow-go/internal/database"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestNodeCountsAppendPagesButReplaceConsolidatedOutput(t *testing.T) {
	url := os.Getenv("CONTROL_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("CONTROL_TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1 // Keep the temporary table scoped to this one connection.
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err = pool.Exec(ctx, `CREATE TEMP TABLE node_runs (LIKE public.node_runs INCLUDING ALL)`); err != nil {
		t.Fatal(err)
	}
	a := &Activities{DB: &database.DB{Pool: pool}}
	started := time.Now()
	write := func(status string, count int, appendPage bool, want int) {
		t.Helper()
		if err := a.recordNodeRun(ctx, "test-counts", "src", "00000000-0000-0000-0000-000000000001", status, started, time.Millisecond, count, "", appendPage); err != nil {
			t.Fatal(err)
		}
		var got int
		if err := pool.QueryRow(ctx, `SELECT COALESCE(record_count,0) FROM node_runs WHERE execution_id='test-counts'`).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s count=%d append=%v: got %d, want %d", status, count, appendPage, got, want)
		}
	}
	write("success", 6, true, 6)
	write("success", 6, true, 12)
	write("success", 0, true, 12)
	write("success", 12, false, 12) // Consolidating pages must not report 24.
	write("success", 12, false, 12) // Re-recording a complete merge is not another page.
	write("failed", 0, false, 0)
	write("success", 3, true, 3) // A retry after failure starts a fresh count.
}
