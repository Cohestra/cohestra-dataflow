package connectors

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSnowflakeWriteStatement(t *testing.T) {
	batch := []map[string]interface{}{{"id": 1, "name": "a", "qty": 2}, {"id": 2, "name": "b", "qty": 3}}
	columns := []string{"id", "name", "qty"}

	query, args := snowflakeWriteStatement("db.sch.orders", columns, nil, batch)
	require.Equal(t, "INSERT INTO db.sch.orders (id,name,qty) VALUES (?,?,?),(?,?,?)", query)
	require.Equal(t, []interface{}{1, "a", 2, 2, "b", 3}, args)

	query, args = snowflakeWriteStatement("db.sch.orders", columns, []string{"id"}, batch)
	require.Equal(t, "MERGE INTO db.sch.orders AS t USING (SELECT column1 AS id, column2 AS name, column3 AS qty FROM VALUES (?,?,?),(?,?,?)) AS s"+
		" ON t.id = s.id WHEN MATCHED THEN UPDATE SET t.name = s.name, t.qty = s.qty"+
		" WHEN NOT MATCHED THEN INSERT (id,name,qty) VALUES (s.id,s.name,s.qty)", query)
	require.Equal(t, []interface{}{1, "a", 2, 2, "b", 3}, args)

	// Composite key; when every column is a key there is nothing to update.
	query, _ = snowflakeWriteStatement("orders", []string{"region", "id"}, []string{"region", "id"}, []map[string]interface{}{{"region": "eu", "id": 1}})
	require.Equal(t, "MERGE INTO orders AS t USING (SELECT column1 AS region, column2 AS id FROM VALUES (?,?)) AS s"+
		" ON t.region = s.region AND t.id = s.id WHEN NOT MATCHED THEN INSERT (region,id) VALUES (s.region,s.id)", query)
}

func TestSnowflakeMergeRows(t *testing.T) {
	rows := []map[string]interface{}{
		{"id": float64(1), "v": "first"}, {"id": float64(2), "v": "only"}, {"id": float64(1), "v": "last"},
	}
	merged, err := snowflakeMergeRows(rows, []string{"id", "v"}, []string{"id"})
	require.NoError(t, err)
	// The later row for a key wins and keeps the key's first position.
	require.Equal(t, []map[string]interface{}{{"id": float64(1), "v": "last"}, {"id": float64(2), "v": "only"}}, merged)

	// Key values of different types stay distinct (1 is not "1").
	merged, err = snowflakeMergeRows([]map[string]interface{}{{"id": float64(1)}, {"id": "1"}}, []string{"id"}, []string{"id"})
	require.NoError(t, err)
	require.Len(t, merged, 2)

	for name, test := range map[string]struct {
		rows    []map[string]interface{}
		keys    []string
		message string
	}{
		"null key":    {[]map[string]interface{}{{"id": nil, "v": 1}}, []string{"id"}, "no value for conflictKey"},
		"missing key": {[]map[string]interface{}{{"v": 1}}, []string{"id"}, "not in the records"},
		"injection":   {[]map[string]interface{}{{"id": 1}}, []string{"id; DROP TABLE x"}, "invalid conflictKey"},
	} {
		columns := allColumns(test.rows)
		_, err := snowflakeMergeRows(test.rows, columns, test.keys)
		require.ErrorContains(t, err, test.message, name)
	}
}

// recordingDriver is a database/sql driver that records statements, commits
// and rollbacks, and can fail the nth statement.
type recordingDriver struct {
	mu        sync.Mutex
	queries   []string
	commits   int
	rollbacks int
	failOn    int
}

func (d *recordingDriver) Open(string) (driver.Conn, error) { return &recordingConn{d}, nil }

type recordingConn struct{ d *recordingDriver }

func (c *recordingConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("not used") }
func (c *recordingConn) Close() error                        { return nil }
func (c *recordingConn) Begin() (driver.Tx, error)           { return &recordingTx{c.d}, nil }
func (c *recordingConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	c.d.mu.Lock()
	defer c.d.mu.Unlock()
	c.d.queries = append(c.d.queries, query)
	if c.d.failOn == len(c.d.queries) {
		return nil, errors.New("warehouse rejected the batch")
	}
	return driver.RowsAffected(0), nil
}

type recordingTx struct{ d *recordingDriver }

func (t *recordingTx) Commit() error   { t.d.mu.Lock(); t.d.commits++; t.d.mu.Unlock(); return nil }
func (t *recordingTx) Rollback() error { t.d.mu.Lock(); t.d.rollbacks++; t.d.mu.Unlock(); return nil }

var registerRecording sync.Once

func TestSnowflakeMergeBatchesCommitTogether(t *testing.T) {
	recorder := &recordingDriver{}
	registerRecording.Do(func() { sql.Register("recording", &switchDriver{}) })
	current.Store(recorder)
	db, err := sql.Open("recording", "")
	require.NoError(t, err)
	defer db.Close()

	rows := make([]map[string]interface{}, 0, dbBatchSize+100)
	for i := 0; i < dbBatchSize+100; i++ {
		rows = append(rows, map[string]interface{}{"id": float64(i), "v": "x"})
	}
	write := func() error {
		return insertBatchesInTx(context.Background(), db, len(rows), func(start, end int) (string, []interface{}) {
			return snowflakeWriteStatement("orders", []string{"id", "v"}, []string{"id"}, rows[start:end])
		})
	}
	require.NoError(t, write())
	require.Len(t, recorder.queries, 2)
	for _, query := range recorder.queries {
		require.True(t, strings.HasPrefix(query, "MERGE INTO orders"), query)
	}
	require.Equal(t, 1, recorder.commits)

	// A failing batch rolls back the whole write: nothing is half-merged.
	failing := &recordingDriver{failOn: 2}
	current.Store(failing)
	db.SetMaxIdleConns(0) // reconnect through the new recorder
	require.ErrorContains(t, write(), "warehouse rejected")
	require.Equal(t, 0, failing.commits)
	require.Equal(t, 1, failing.rollbacks)
}

// switchDriver lets each test point the registered driver at a new recorder.
type switchDriver struct{}

var current atomic.Pointer[recordingDriver]

func (switchDriver) Open(name string) (driver.Conn, error) { return current.Load().Open(name) }
