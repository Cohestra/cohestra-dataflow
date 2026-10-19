package connectors

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// These tests run against a real Postgres when CONNECTOR_TEST_POSTGRES_URL is
// set (any scratch database; they create and drop their own tables).
func connectorTestPool(t *testing.T) (*Runtime, string, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("CONNECTOR_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("CONNECTOR_TEST_POSTGRES_URL is not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	id := "test-" + t.Name()
	connectorPools.mu.Lock()
	connectorPools.postgres[id] = pool
	connectorPools.mu.Unlock()
	t.Cleanup(func() {
		connectorPools.mu.Lock()
		delete(connectorPools.postgres, id)
		connectorPools.mu.Unlock()
		pool.Close()
	})
	return &Runtime{}, id, pool
}

func TestPostgresFetchNeverDropsRowsSharingTheBoundaryCursor(t *testing.T) {
	runtime, id, pool := connectorTestPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS fetch_ties; CREATE TABLE fetch_ties (id int PRIMARY KEY, updated int NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DROP TABLE IF EXISTS fetch_ties`) })
	// Cursor values with ties that straddle every page boundary at pageSize 3.
	updated := []int{1, 1, 2, 2, 2, 3, 3, 4, 4, 4, 4, 5}
	for i, value := range updated {
		if _, err := pool.Exec(ctx, `INSERT INTO fetch_ties VALUES ($1,$2)`, i+1, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, keyColumn := range []string{"", "id"} {
		t.Run("keyColumn="+keyColumn, func(t *testing.T) {
			seen := map[int32]int{}
			cursor := map[string]interface{}{}
			pageSize := 3
			if keyColumn == "" {
				// Four rows share updated=4, so unkeyed paging needs pageSize > 4.
				pageSize = 5
			}
			for pages := 0; ; pages++ {
				if pages > 20 {
					t.Fatal("paging did not finish")
				}
				result, err := runtime.postgresFetch(ctx, SourceParams{Config: map[string]interface{}{"connectionId": id, "table": "fetch_ties", "cursorColumn": "updated", "keyColumn": keyColumn, "pageSize": pageSize}, Cursor: cursor})
				if err != nil {
					t.Fatal(err)
				}
				for _, record := range result.Records {
					seen[record.(map[string]interface{})["id"].(int32)]++
				}
				cursor = result.NextCursor
				if !result.HasMore {
					break
				}
			}
			if len(seen) != len(updated) {
				t.Fatalf("read %d distinct rows, want %d: %v", len(seen), len(updated), seen)
			}
			for row, count := range seen {
				if count != 1 {
					t.Fatalf("row %d read %d times", row, count)
				}
			}
		})
	}
	// Without a key column, a page that cannot advance past a tie says so.
	_, err := runtime.postgresFetch(ctx, SourceParams{Config: map[string]interface{}{"connectionId": id, "table": "fetch_ties", "cursorColumn": "updated", "pageSize": 3}, Cursor: map[string]interface{}{"value": 3}})
	if !isPermanent(err, InvalidConfigError) {
		t.Fatalf("all-tie page error = %v", err)
	}
}

func TestPostgresSinkWritesAllBatchesOrNone(t *testing.T) {
	runtime, id, pool := connectorTestPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS sink_tx; CREATE TABLE sink_tx (id int PRIMARY KEY, name text NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DROP TABLE IF EXISTS sink_tx`) })
	rows := []interface{}{}
	for i := 1; i <= dbBatchSize+10; i++ {
		rows = append(rows, map[string]interface{}{"id": i, "name": fmt.Sprintf("row-%d", i)})
	}
	// The last row violates NOT NULL in the second batch: nothing may persist.
	rows[len(rows)-1].(map[string]interface{})["name"] = nil
	if _, _, err := runtime.postgresSink(ctx, rows, map[string]interface{}{"connectionId": id, "table": "sink_tx"}, HandlerContext{}); err == nil {
		t.Fatal("expected the failing batch to fail the sink")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sink_tx`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("%d rows from earlier batches were committed by a failed attempt", count)
	}
	rows[len(rows)-1].(map[string]interface{})["name"] = "fixed"
	if _, _, err := runtime.postgresSink(ctx, rows, map[string]interface{}{"connectionId": id, "table": "sink_tx"}, HandlerContext{}); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sink_tx`).Scan(&count); err != nil || count != len(rows) {
		t.Fatalf("rows after retry = %d, %v; want %d", count, err, len(rows))
	}
}
