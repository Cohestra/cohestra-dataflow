package connectors

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/snowflakedb/gosnowflake/v2"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/dataflow-poc/workflow-go/internal/model"
	"github.com/dataflow-poc/workflow-go/internal/security"
)

var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var snowflakePath = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)

// connPoolCap bounds how many live connections each connector instance (one
// tenant's one postgres/mysql/snowflake/mongo credential) may hold in this
// worker process. Kept at 5, not something like 20: a worker pod runs many
// concurrent pipeline executions spanning many tenants, so a small per-tenant
// cap prevents one noisy tenant from exhausting the source database's
// max_connections. Override with DB_POOL_MAX_CONNS for deployments with
// known extra headroom.
var connPoolCap = envInt32("DB_POOL_MAX_CONNS", 5)

func envInt32(name string, fallback int32) int32 {
	if value, err := strconv.ParseInt(os.Getenv(name), 10, 32); err == nil && value > 0 {
		return int32(value)
	}
	return fallback
}

// dbBatchSize caps rows per multi-row INSERT/BulkWrite call. PostgreSQL batches
// are reduced further based on their column count to stay within the protocol's
// 65535 bind-parameter limit.
const dbBatchSize = 500
const postgresMaxBindParams = 65535

func postgresBatchRows(columnCount int) (int, error) {
	if columnCount < 1 {
		return 0, fmt.Errorf("sink.postgres: records must contain at least one column")
	}
	rows := postgresMaxBindParams / columnCount
	if rows < 1 {
		return 0, fmt.Errorf("sink.postgres: %d columns exceed the bind-parameter limit", columnCount)
	}
	return min(dbBatchSize, rows), nil
}

// connCache is a worker-scoped pool cache keyed by connector-instance ID
// (connectionId / connector_instances.id): repeated fetch/sink calls for the
// SAME instance reuse one bounded pool instead of dialing a fresh connection
// per page or batch. One process runs one Runtime, so a package-level cache
// is effectively worker-scoped.
type connCache struct {
	mu        sync.Mutex
	postgres  map[string]*pgxpool.Pool
	mysql     map[string]*sql.DB
	snowflake map[string]*sql.DB
	mongo     map[string]*mongoConn
}
type mongoConn struct {
	client   *mongo.Client
	database string
}

var connectorPools = &connCache{
	postgres:  map[string]*pgxpool.Pool{},
	mysql:     map[string]*sql.DB{},
	snowflake: map[string]*sql.DB{},
	mongo:     map[string]*mongoConn{},
}

// CloseConnectorPools releases every cached database connection pool. Call it
// once on worker shutdown (e.g. alongside db.Close()/temporal.Close() in
// cmd/activity-worker/main.go's defer chain).
func (r *Runtime) CloseConnectorPools() {
	connectorPools.mu.Lock()
	defer connectorPools.mu.Unlock()
	for _, pool := range connectorPools.postgres {
		pool.Close()
	}
	for _, db := range connectorPools.mysql {
		_ = db.Close()
	}
	for _, db := range connectorPools.snowflake {
		_ = db.Close()
	}
	for _, conn := range connectorPools.mongo {
		_ = conn.client.Disconnect(context.Background())
	}
	connectorPools.postgres = map[string]*pgxpool.Pool{}
	connectorPools.mysql = map[string]*sql.DB{}
	connectorPools.snowflake = map[string]*sql.DB{}
	connectorPools.mongo = map[string]*mongoConn{}
}

func boundedPageSize(values ...interface{}) int {
	values = append(values, 1000)
	page := int(firstNumber(values...))
	if page < 1 {
		return 1
	}
	if page > 10000 {
		return 10000
	}
	return page
}

func quoteIdentifier(value string) (string, error) {
	if !identifier.MatchString(value) {
		return "", fmt.Errorf("invalid identifier %q", value)
	}
	return `"` + value + `"`, nil
}
func quotePath(value string) (string, error) {
	parts := strings.Split(value, ".")
	out := make([]string, len(parts))
	for i, part := range parts {
		quoted, err := quoteIdentifier(part)
		if err != nil {
			return "", err
		}
		out[i] = quoted
	}
	return strings.Join(out, "."), nil
}
func recordsMaps(input interface{}) ([]map[string]interface{}, error) {
	raw, err := records(input)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]interface{}, len(raw))
	for i, value := range raw {
		row, ok := value.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("records must be objects")
		}
		out[i] = row
	}
	return out, nil
}

func (r *Runtime) registerDatabases() {
	r.Sources["postgres.fetch"] = r.postgresFetch
	r.Handlers["sink.postgres"] = r.postgresSink
	r.Sources["mysql.fetch"] = r.mysqlFetch
	r.Handlers["sink.mysql"] = r.mysqlSink
	r.Sources["mongodb.fetch"] = r.mongoFetch
	r.Handlers["sink.mongodb"] = r.mongoSink
	r.Sources["snowflake.fetch"] = r.snowflakeFetch
	r.Handlers["sink.snowflake"] = r.snowflakeSink
	r.Handlers["sink.clickhouse"] = r.clickhouseSink
}
func (r *Runtime) postgresConnection(ctx context.Context, id string) (*pgxpool.Pool, error) {
	connectorPools.mu.Lock()
	if pool, ok := connectorPools.postgres[id]; ok {
		connectorPools.mu.Unlock()
		return pool, nil
	}
	connectorPools.mu.Unlock()
	row, err := r.credential(ctx, id)
	if err != nil {
		return nil, err
	}
	cfg, _ := row["extra"].(map[string]interface{})
	secret, _ := row["secret_value"].(map[string]interface{})
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s", url.QueryEscape(stringValue(cfg["user"])), url.QueryEscape(stringValue(secret["password"])), stringValue(cfg["host"]), int(firstNumber(cfg["port"], 5432)), url.PathEscape(stringValue(cfg["database"])), firstString(cfg["sslMode"], "disable"))
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	poolConfig.MaxConns = connPoolCap
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, err
	}
	connectorPools.mu.Lock()
	defer connectorPools.mu.Unlock()
	if existing, ok := connectorPools.postgres[id]; ok {
		pool.Close()
		return existing, nil
	}
	connectorPools.postgres[id] = pool
	return pool, nil
}

// incrementalPlan is one page query for a cursor-paged SQL source.
//
// Paging on the cursor column alone with "cursor > last" drops rows that share
// the last cursor value but did not fit in the page. Two strategies avoid that:
//   - keyColumn set (a unique column such as the primary key): keyset paging on
//     (cursor, key), so ties are ordered and resumed exactly.
//   - no keyColumn: the page is cut before any rows that tie with the first
//     row of the next page, so the next page re-reads those rows in full.
//     More than pageSize rows sharing one cursor value then needs keyColumn.
type incrementalPlan struct {
	query string
	args  []interface{}
	keyed bool
}

type sqlDialect struct {
	quote       func(string) (string, error)
	placeholder func(n int) string
}

var postgresDialect = sqlDialect{quote: quoteIdentifier, placeholder: func(n int) string { return fmt.Sprintf("$%d", n) }}
var mysqlDialect = sqlDialect{quote: func(value string) (string, error) {
	if !identifier.MatchString(value) {
		return "", fmt.Errorf("invalid identifier %q", value)
	}
	return "`" + value + "`", nil
}, placeholder: func(int) string { return "?" }}

func planIncrementalPage(d sqlDialect, columns, table, cursorColumn, keyColumn string, cursor map[string]interface{}, ingestion *model.IngestionConfig, page int) (incrementalPlan, error) {
	quotedCursor, err := d.quote(cursorColumn)
	if err != nil {
		return incrementalPlan{}, configError("cursorColumn must be a column name")
	}
	quotedKey := ""
	if keyColumn != "" {
		if quotedKey, err = d.quote(keyColumn); err != nil {
			return incrementalPlan{}, configError("keyColumn must be a column name")
		}
	}
	args := []interface{}{}
	arg := func(value interface{}) string {
		args = append(args, value)
		return d.placeholder(len(args))
	}
	where := []string{}
	switch {
	case cursor["value"] != nil && quotedKey != "" && cursor["key"] != nil:
		where = append(where, fmt.Sprintf("(%s,%s)>(%s,%s)", quotedCursor, quotedKey, arg(cursor["value"]), arg(cursor["key"])))
	case cursor["value"] != nil && quotedKey != "":
		// First keyed page after an unkeyed checkpoint: re-read the boundary value
		// rather than risk skipping rows that tie with it.
		where = append(where, fmt.Sprintf("%s>=%s", quotedCursor, arg(cursor["value"])))
	case cursor["value"] != nil:
		where = append(where, fmt.Sprintf("%s>%s", quotedCursor, arg(cursor["value"])))
	case ingestion != nil && ingestion.Mode == "backfill":
		where = append(where, fmt.Sprintf("%s>=%s", quotedCursor, arg(ingestion.BackfillStart)))
	}
	if ingestion != nil && ingestion.BackfillEnd != "" {
		where = append(where, fmt.Sprintf("%s<%s", quotedCursor, arg(ingestion.BackfillEnd)))
	}
	query := fmt.Sprintf("SELECT %s FROM %s", columns, table)
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	order := quotedCursor + " ASC"
	if quotedKey != "" {
		order += "," + quotedKey + " ASC"
	}
	query += " ORDER BY " + order + " LIMIT " + arg(page+1)
	return incrementalPlan{query: query, args: args, keyed: quotedKey != ""}, nil
}

// finishIncrementalPage turns a page+1 row result into the page to emit and
// its checkpoint. Unkeyed pages are cut before trailing rows that tie with the
// first row of the next page; those rows are read again by the next page.
func finishIncrementalPage(values []interface{}, page int, cursorColumn, keyColumn string, cursor map[string]interface{}, keyed bool) ([]interface{}, map[string]interface{}, bool, error) {
	hasMore := len(values) > page
	if hasMore {
		lookahead := rowValue(values[page], cursorColumn)
		values = values[:page]
		if !keyed {
			end := len(values)
			for end > 0 && sameCursorValue(rowValue(values[end-1], cursorColumn), lookahead) {
				end--
			}
			if end == 0 {
				return nil, nil, false, configError("more than %d rows share %s value %v; set keyColumn to a unique column or raise pageSize", page, cursorColumn, lookahead)
			}
			values = values[:end]
		}
	}
	next := cloneMap(cursor)
	if len(values) > 0 {
		last := values[len(values)-1]
		next["value"] = rowValue(last, cursorColumn)
		if keyed {
			next["key"] = rowValue(last, keyColumn)
		}
	}
	return values, next, hasMore, nil
}

func rowValue(row interface{}, column string) interface{} {
	object, _ := row.(map[string]interface{})
	return object[column]
}

func sameCursorValue(a, b interface{}) bool {
	if at, ok := a.(time.Time); ok {
		bt, ok := b.(time.Time)
		return ok && at.Equal(bt)
	}
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

func (r *Runtime) postgresFetch(ctx context.Context, p SourceParams) (SourceResult, error) {
	table, err := quotePath(stringValue(p.Config["table"]))
	if err != nil {
		return SourceResult{}, configError("postgres.fetch: %v", err)
	}
	cursorColumn := stringValue(p.Config["cursorColumn"])
	if cursorColumn == "" {
		return SourceResult{}, configError("postgres.fetch: cursorColumn required")
	}
	page := boundedPageSize(func() interface{} {
		if p.Ingestion != nil {
			return p.Ingestion.PageSize
		}
		return nil
	}(), p.Config["pageSize"])
	columns := "*"
	if value := stringValue(p.Config["columns"]); value != "" && value != "*" {
		items := []string{}
		for _, item := range strings.Split(value, ",") {
			quoted, err := quoteIdentifier(strings.TrimSpace(item))
			if err != nil {
				return SourceResult{}, configError("postgres.fetch: %v", err)
			}
			items = append(items, quoted)
		}
		columns = strings.Join(items, ",")
	}
	keyColumn := stringValue(p.Config["keyColumn"])
	plan, err := planIncrementalPage(postgresDialect, columns, table, cursorColumn, keyColumn, p.Cursor, p.Ingestion, page)
	if err != nil {
		return SourceResult{}, err
	}
	pool, err := r.postgresConnection(ctx, stringValue(p.Config["connectionId"]))
	if err != nil {
		return SourceResult{}, err
	}
	rows, err := pool.Query(ctx, plan.query, plan.args...)
	if err != nil {
		return SourceResult{}, err
	}
	values, err := pgRows(rows)
	if err != nil {
		return SourceResult{}, err
	}
	values, next, hasMore, err := finishIncrementalPage(values, page, cursorColumn, keyColumn, p.Cursor, plan.keyed)
	if err != nil {
		return SourceResult{}, err
	}
	return SourceResult{Records: values, NextCursor: next, HasMore: hasMore}, nil
}
func pgRows(rows pgx.Rows) ([]interface{}, error) {
	defer rows.Close()
	fields := rows.FieldDescriptions()
	out := []interface{}{}
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, err
		}
		row := map[string]interface{}{}
		for i, value := range values {
			row[string(fields[i].Name)] = value
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
func (r *Runtime) postgresSink(ctx context.Context, input interface{}, cfg map[string]interface{}, _ HandlerContext) (interface{}, map[string]interface{}, error) {
	rows, err := recordsMaps(input)
	if err != nil || len(rows) == 0 {
		return nil, nil, err
	}
	table, err := quotePath(stringValue(cfg["table"]))
	if err != nil {
		return nil, nil, err
	}
	columns := allColumns(rows)
	batchSize, err := postgresBatchRows(len(columns))
	if err != nil {
		return nil, nil, err
	}
	quoted := []string{}
	for _, column := range columns {
		value, err := quoteIdentifier(column)
		if err != nil {
			return nil, nil, err
		}
		quoted = append(quoted, value)
	}
	conflict := keyFields(cfg["conflictKey"])
	pool, err := r.postgresConnection(ctx, stringValue(cfg["connectionId"]))
	if err != nil {
		return nil, nil, err
	}
	conflictClause := ""
	if len(conflict) > 0 {
		keys := []string{}
		for _, key := range conflict {
			quotedKey, _ := quoteIdentifier(key)
			keys = append(keys, quotedKey)
		}
		updates := []string{}
		for _, column := range columns {
			if contains(conflict, column) {
				continue
			}
			quotedColumn, _ := quoteIdentifier(column)
			updates = append(updates, quotedColumn+"=EXCLUDED."+quotedColumn)
		}
		conflictClause = " ON CONFLICT (" + strings.Join(keys, ",") + ") DO "
		if len(updates) > 0 {
			conflictClause += "UPDATE SET " + strings.Join(updates, ",")
		} else {
			conflictClause += "NOTHING"
		}
	}
	// Batch rows into multi-row INSERTs (dbBatchSize per statement) instead of
	// one round trip per row. All batches commit together, so a retried attempt
	// never re-inserts batches an earlier failed attempt already wrote.
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	for start := 0; start < len(rows); start += batchSize {
		end := start + batchSize
		if end > len(rows) {
			end = len(rows)
		}
		batch := rows[start:end]
		groups := make([]string, len(batch))
		args := make([]interface{}, 0, len(batch)*len(columns))
		for i, row := range batch {
			placeholders := make([]string, len(columns))
			for j, column := range columns {
				args = append(args, row[column])
				placeholders[j] = fmt.Sprintf("$%d", len(args))
			}
			groups[i] = "(" + strings.Join(placeholders, ",") + ")"
		}
		query := fmt.Sprintf("INSERT INTO %s (%s) VALUES %s", table, strings.Join(quoted, ","), strings.Join(groups, ",")) + conflictClause
		if _, err = tx.Exec(ctx, query, args...); err != nil {
			return nil, nil, err
		}
	}
	return nil, nil, tx.Commit(ctx)
}
func allColumns(rows []map[string]interface{}) []string {
	set := map[string]bool{}
	for _, row := range rows {
		for key := range row {
			set[key] = true
		}
	}
	out := []string{}
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func (r *Runtime) mysqlDB(ctx context.Context, id string) (*sql.DB, error) {
	connectorPools.mu.Lock()
	if db, ok := connectorPools.mysql[id]; ok {
		connectorPools.mu.Unlock()
		return db, nil
	}
	connectorPools.mu.Unlock()
	row, err := r.credential(ctx, id)
	if err != nil {
		return nil, err
	}
	cfg, _ := row["extra"].(map[string]interface{})
	secret, _ := row["secret_value"].(map[string]interface{})
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true", stringValue(cfg["user"]), stringValue(secret["password"]), stringValue(cfg["host"]), int(firstNumber(cfg["port"], 3306)), stringValue(cfg["database"]))
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(int(connPoolCap))
	db.SetMaxIdleConns(int(connPoolCap))
	db.SetConnMaxLifetime(30 * time.Minute)
	if err = db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	connectorPools.mu.Lock()
	defer connectorPools.mu.Unlock()
	if existing, ok := connectorPools.mysql[id]; ok {
		_ = db.Close()
		return existing, nil
	}
	connectorPools.mysql[id] = db
	return db, nil
}
func (r *Runtime) mysqlFetch(ctx context.Context, p SourceParams) (SourceResult, error) {
	table := stringValue(p.Config["table"])
	column := stringValue(p.Config["cursorColumn"])
	if !identifier.MatchString(table) || !identifier.MatchString(column) {
		return SourceResult{}, configError("mysql.fetch: invalid table or cursor column")
	}
	page := boundedPageSize(p.Config["pageSize"])
	keyColumn := stringValue(p.Config["keyColumn"])
	// mysql.fetch has no backfill window; it pages from the saved cursor only.
	plan, err := planIncrementalPage(mysqlDialect, "*", "`"+table+"`", column, keyColumn, p.Cursor, nil, page)
	if err != nil {
		return SourceResult{}, err
	}
	db, err := r.mysqlDB(ctx, stringValue(p.Config["connectionId"]))
	if err != nil {
		return SourceResult{}, err
	}
	result, err := db.QueryContext(ctx, plan.query, plan.args...)
	if err != nil {
		return SourceResult{}, err
	}
	values, err := sqlRows(result)
	if err != nil {
		return SourceResult{}, err
	}
	values, next, hasMore, err := finishIncrementalPage(values, page, column, keyColumn, p.Cursor, plan.keyed)
	if err != nil {
		return SourceResult{}, err
	}
	return SourceResult{Records: values, NextCursor: next, HasMore: hasMore}, nil
}
func sqlRows(rows *sql.Rows) ([]interface{}, error) {
	defer rows.Close()
	columns, _ := rows.Columns()
	out := []interface{}{}
	for rows.Next() {
		values := make([]interface{}, len(columns))
		pointers := make([]interface{}, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		row := map[string]interface{}{}
		for i, value := range values {
			if b, ok := value.([]byte); ok {
				value = string(b)
			}
			row[columns[i]] = value
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
func (r *Runtime) mysqlSink(ctx context.Context, input interface{}, cfg map[string]interface{}, _ HandlerContext) (interface{}, map[string]interface{}, error) {
	rows, err := recordsMaps(input)
	if err != nil || len(rows) == 0 {
		return nil, nil, err
	}
	table := stringValue(cfg["table"])
	if !identifier.MatchString(table) {
		return nil, nil, fmt.Errorf("sink.mysql: invalid table")
	}
	columns := allColumns(rows)
	db, err := r.mysqlDB(ctx, stringValue(cfg["connectionId"]))
	if err != nil {
		return nil, nil, err
	}
	quoted := []string{}
	for _, column := range columns {
		if !identifier.MatchString(column) {
			return nil, nil, fmt.Errorf("invalid column")
		}
		quoted = append(quoted, "`"+column+"`")
	}
	rowPlaceholder := "(" + strings.TrimRight(strings.Repeat("?,", len(columns)), ",") + ")"
	keys := keyFields(cfg["primaryKey"])
	updates := []string{}
	for _, column := range columns {
		if !contains(keys, column) {
			updates = append(updates, "`"+column+"`=VALUES(`"+column+"`)")
		}
	}
	updateClause := ""
	if len(updates) > 0 {
		updateClause = " ON DUPLICATE KEY UPDATE " + strings.Join(updates, ",")
	}
	// Batch rows into multi-row INSERTs (dbBatchSize per statement) instead of
	// one round trip per row, committed together so a retry cannot duplicate
	// batches from an earlier failed attempt.
	return nil, nil, insertBatchesInTx(ctx, db, len(rows), func(start, end int) (string, []interface{}) {
		batch := rows[start:end]
		groups := make([]string, len(batch))
		args := make([]interface{}, 0, len(batch)*len(columns))
		for i, row := range batch {
			groups[i] = rowPlaceholder
			for _, column := range columns {
				args = append(args, row[column])
			}
		}
		return fmt.Sprintf("INSERT INTO `%s` (%s) VALUES %s", table, strings.Join(quoted, ","), strings.Join(groups, ",")) + updateClause, args
	})
}

// insertBatchesInTx runs one INSERT per dbBatchSize rows inside a single
// database/sql transaction and commits only when every batch succeeded.
func insertBatchesInTx(ctx context.Context, db *sql.DB, total int, statement func(start, end int) (string, []interface{})) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	for start := 0; start < total; start += dbBatchSize {
		end := min(start+dbBatchSize, total)
		query, args := statement(start, end)
		if _, err = tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *Runtime) mongoClient(ctx context.Context, id string) (*mongo.Client, string, error) {
	connectorPools.mu.Lock()
	if conn, ok := connectorPools.mongo[id]; ok {
		connectorPools.mu.Unlock()
		return conn.client, conn.database, nil
	}
	connectorPools.mu.Unlock()
	row, err := r.credential(ctx, id)
	if err != nil {
		return nil, "", err
	}
	cfg, _ := row["extra"].(map[string]interface{})
	secret, _ := row["secret_value"].(map[string]interface{})
	auth := ""
	if cfg["user"] != nil {
		auth = url.QueryEscape(stringValue(cfg["user"])) + ":" + url.QueryEscape(stringValue(secret["password"])) + "@"
	}
	authSource := url.QueryEscape(firstString(cfg["authSource"], "admin"))
	var uri string
	// SRV is a DNS discovery mode, not a TLS mode: use it when explicitly asked
	// (srv=true) or for TLS hosts with no configured port (Atlas-style). TLS
	// deployments with an explicit port (self-managed, DocumentDB) connect via
	// plain mongodb:// with tls=true.
	if truthy(cfg["srv"]) || (truthy(cfg["tls"]) && cfg["port"] == nil) {
		uri = fmt.Sprintf("mongodb+srv://%s%s/?authSource=%s&tls=true", auth, stringValue(cfg["host"]), authSource)
	} else {
		uri = fmt.Sprintf("mongodb://%s%s:%d/?authSource=%s", auth, stringValue(cfg["host"]), int(firstNumber(cfg["port"], 27017)), authSource)
		if truthy(cfg["tls"]) {
			uri += "&tls=true"
		}
	}
	client, err := mongo.Connect(options.Client().ApplyURI(uri).SetServerSelectionTimeout(10 * time.Second).SetMaxPoolSize(uint64(connPoolCap)))
	if err != nil {
		return nil, "", err
	}
	if err = client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, "", err
	}
	database := stringValue(cfg["database"])
	connectorPools.mu.Lock()
	defer connectorPools.mu.Unlock()
	if existing, ok := connectorPools.mongo[id]; ok {
		_ = client.Disconnect(context.Background())
		return existing.client, existing.database, nil
	}
	connectorPools.mongo[id] = &mongoConn{client: client, database: database}
	return client, database, nil
}
func (r *Runtime) mongoFetch(ctx context.Context, p SourceParams) (SourceResult, error) {
	client, database, err := r.mongoClient(ctx, stringValue(p.Config["connectionId"]))
	if err != nil {
		return SourceResult{}, err
	}
	collection := client.Database(database).Collection(stringValue(p.Config["collection"]))
	field := firstString(p.Config["cursorField"], "_id")
	filter, err := mongoPageFilter(field, p.Cursor)
	if err != nil {
		return SourceResult{}, err
	}
	sort := bson.D{{Key: field, Value: 1}}
	if field != "_id" {
		// _id breaks ties so rows sharing a cursor value are paged in order.
		sort = append(sort, bson.E{Key: "_id", Value: 1})
	}
	page := boundedPageSize(p.Config["pageSize"])
	cursor, err := collection.Find(ctx, filter, options.Find().SetSort(sort).SetLimit(int64(page+1)))
	if err != nil {
		return SourceResult{}, err
	}
	var docs []bson.M
	if err = cursor.All(ctx, &docs); err != nil {
		return SourceResult{}, err
	}
	hasMore := len(docs) > page
	if hasMore {
		docs = docs[:page]
	}
	values := make([]interface{}, len(docs))
	for i, doc := range docs {
		body, _ := json.Marshal(doc)
		var value map[string]interface{}
		_ = json.Unmarshal(body, &value)
		values[i] = value
	}
	next := cloneMap(p.Cursor)
	if len(docs) > 0 {
		last := docs[len(docs)-1]
		next["value"] = values[len(values)-1].(map[string]interface{})[field]
		if next["valueExtJson"], err = mongoExtJSON(last[field]); err != nil {
			return SourceResult{}, err
		}
		if next["idExtJson"], err = mongoExtJSON(last["_id"]); err != nil {
			return SourceResult{}, err
		}
	}
	return SourceResult{Records: values, NextCursor: next, HasMore: hasMore}, nil
}

// mongoPageFilter resumes after the checkpointed (cursorField, _id) position.
// The checkpoint keeps both values as Extended JSON so BSON types such as
// ObjectID and dates compare correctly; the plain JSON "value" alone compares a
// string against an ObjectID and matches nothing. Old checkpoints without the
// Extended JSON fields fall back to that plain value.
func mongoPageFilter(field string, cursor map[string]interface{}) (bson.M, error) {
	valueJSON, idJSON := stringValue(cursor["valueExtJson"]), stringValue(cursor["idExtJson"])
	if valueJSON == "" {
		if cursor["value"] != nil {
			value := cursor["value"]
			if hex, ok := value.(string); ok && field == "_id" {
				if id, err := bson.ObjectIDFromHex(hex); err == nil {
					value = id
				}
			}
			return bson.M{field: bson.M{"$gt": value}}, nil
		}
		return bson.M{}, nil
	}
	value, err := mongoFromExtJSON(valueJSON)
	if err != nil {
		return nil, err
	}
	if field == "_id" || idJSON == "" {
		return bson.M{field: bson.M{"$gt": value}}, nil
	}
	id, err := mongoFromExtJSON(idJSON)
	if err != nil {
		return nil, err
	}
	return bson.M{"$or": bson.A{
		bson.M{field: bson.M{"$gt": value}},
		bson.M{field: value, "_id": bson.M{"$gt": id}},
	}}, nil
}

func mongoExtJSON(value interface{}) (string, error) {
	body, err := bson.MarshalExtJSON(bson.M{"v": value}, true, false)
	return string(body), err
}

func mongoFromExtJSON(value string) (interface{}, error) {
	var doc bson.M
	if err := bson.UnmarshalExtJSON([]byte(value), true, &doc); err != nil {
		return nil, fmt.Errorf("mongodb.fetch: invalid saved cursor: %w", err)
	}
	return doc["v"], nil
}
func (r *Runtime) mongoSink(ctx context.Context, input interface{}, cfg map[string]interface{}, _ HandlerContext) (interface{}, map[string]interface{}, error) {
	rows, err := recordsMaps(input)
	if err != nil || len(rows) == 0 {
		return nil, nil, err
	}
	client, database, err := r.mongoClient(ctx, stringValue(cfg["connectionId"]))
	if err != nil {
		return nil, nil, err
	}
	collection := client.Database(database).Collection(stringValue(cfg["collection"]))
	key := firstString(cfg["keyField"], "_id")
	// Batch upserts into BulkWrite calls (dbBatchSize per call) instead of one
	// round trip per row.
	for start := 0; start < len(rows); start += dbBatchSize {
		end := start + dbBatchSize
		if end > len(rows) {
			end = len(rows)
		}
		batch := rows[start:end]
		models := make([]mongo.WriteModel, len(batch))
		for i, row := range batch {
			models[i] = mongo.NewReplaceOneModel().SetFilter(bson.M{key: row[key]}).SetReplacement(row).SetUpsert(true)
		}
		if _, err = collection.BulkWrite(ctx, models); err != nil {
			return nil, nil, err
		}
	}
	return nil, nil, nil
}

func (r *Runtime) snowflakeDB(ctx context.Context, id string) (*sql.DB, error) {
	connectorPools.mu.Lock()
	if db, ok := connectorPools.snowflake[id]; ok {
		connectorPools.mu.Unlock()
		return db, nil
	}
	connectorPools.mu.Unlock()
	row, err := r.credential(ctx, id)
	if err != nil {
		return nil, err
	}
	cfg, _ := row["extra"].(map[string]interface{})
	secret, _ := row["secret_value"].(map[string]interface{})
	sf := &gosnowflake.Config{Account: stringValue(cfg["account"]), User: stringValue(cfg["user"]), Password: stringValue(secret["password"]), Database: stringValue(cfg["database"]), Schema: firstString(cfg["schema"], "PUBLIC"), Warehouse: stringValue(cfg["warehouse"]), Role: stringValue(cfg["role"])}
	dsn, err := gosnowflake.DSN(sf)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("snowflake", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(int(connPoolCap))
	db.SetMaxIdleConns(int(connPoolCap))
	db.SetConnMaxLifetime(30 * time.Minute)
	if err = db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	connectorPools.mu.Lock()
	defer connectorPools.mu.Unlock()
	if existing, ok := connectorPools.snowflake[id]; ok {
		_ = db.Close()
		return existing, nil
	}
	connectorPools.snowflake[id] = db
	return db, nil
}
func (r *Runtime) snowflakeFetch(ctx context.Context, p SourceParams) (SourceResult, error) {
	table := stringValue(p.Config["table"])
	if !snowflakePath.MatchString(table) {
		return SourceResult{}, fmt.Errorf("snowflake.fetch: invalid table")
	}
	page := boundedPageSize(p.Config["pageSize"])
	query := "SELECT * FROM " + table
	args := []interface{}{}
	column := stringValue(p.Config["cursorColumn"])
	if column != "" && !identifier.MatchString(column) {
		return SourceResult{}, fmt.Errorf("snowflake.fetch: invalid cursor column")
	}
	if column != "" && p.Cursor["value"] != nil {
		query += " WHERE " + column + ">?"
		args = append(args, p.Cursor["value"])
	}
	if column != "" {
		query += " ORDER BY " + column
	}
	query += " LIMIT " + strconv.Itoa(page+1)
	db, err := r.snowflakeDB(ctx, stringValue(p.Config["connectionId"]))
	if err != nil {
		return SourceResult{}, err
	}
	result, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return SourceResult{}, err
	}
	values, err := sqlRows(result)
	if err != nil {
		return SourceResult{}, err
	}
	if column == "" {
		// Without a cursor column there is no stable order to resume from: the
		// next page would re-read an arbitrary LIMIT window.
		if len(values) > page {
			return SourceResult{}, configError("snowflake.fetch: table has more than %d rows; set cursorColumn so pages can be read in order", page)
		}
		return SourceResult{Records: values, NextCursor: cloneMap(p.Cursor), HasMore: false}, nil
	}
	values, next, hasMore, err := finishIncrementalPage(values, page, column, "", p.Cursor, false)
	if err != nil {
		return SourceResult{}, err
	}
	return SourceResult{Records: values, NextCursor: next, HasMore: hasMore}, nil
}
func (r *Runtime) snowflakeSink(ctx context.Context, input interface{}, cfg map[string]interface{}, _ HandlerContext) (interface{}, map[string]interface{}, error) {
	rows, err := recordsMaps(input)
	if err != nil || len(rows) == 0 {
		return nil, nil, err
	}
	table := stringValue(cfg["table"])
	if !snowflakePath.MatchString(table) {
		return nil, nil, fmt.Errorf("sink.snowflake: invalid table")
	}
	columns := allColumns(rows)
	for _, column := range columns {
		if !identifier.MatchString(column) {
			return nil, nil, fmt.Errorf("sink.snowflake: invalid column")
		}
	}
	db, err := r.snowflakeDB(ctx, stringValue(cfg["connectionId"]))
	if err != nil {
		return nil, nil, err
	}
	rowPlaceholder := "(" + strings.TrimRight(strings.Repeat("?,", len(columns)), ",") + ")"
	// Batch rows into multi-row INSERTs (dbBatchSize per statement) in one
	// transaction, so a retried attempt does not duplicate committed batches.
	return nil, nil, insertBatchesInTx(ctx, db, len(rows), func(start, end int) (string, []interface{}) {
		batch := rows[start:end]
		groups := make([]string, len(batch))
		args := make([]interface{}, 0, len(batch)*len(columns))
		for i, row := range batch {
			groups[i] = rowPlaceholder
			for _, column := range columns {
				args = append(args, row[column])
			}
		}
		return fmt.Sprintf("INSERT INTO %s (%s) VALUES %s", table, strings.Join(columns, ","), strings.Join(groups, ",")), args
	})
}

func (r *Runtime) clickhouseSink(ctx context.Context, input interface{}, cfg map[string]interface{}, _ HandlerContext) (interface{}, map[string]interface{}, error) {
	rows, err := recordsMaps(input)
	if err != nil || len(rows) == 0 {
		return nil, nil, err
	}
	instance, err := r.credential(ctx, stringValue(cfg["connectionId"]))
	if err != nil {
		return nil, nil, err
	}
	extra, _ := instance["extra"].(map[string]interface{})
	secret, _ := instance["secret_value"].(map[string]interface{})
	table := stringValue(cfg["table"])
	if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)?$`).MatchString(table) {
		return nil, nil, fmt.Errorf("sink.clickhouse: invalid table")
	}
	endpoint := strings.TrimSuffix(stringValue(extra["url"]), "/") + "/?query=" + url.QueryEscape("INSERT INTO "+table+" FORMAT JSONEachRow")
	if _, err := security.ValidateURL(endpoint); err != nil {
		return nil, nil, fmt.Errorf("sink.clickhouse: %w", err)
	}
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	for _, row := range rows {
		_ = encoder.Encode(row)
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	request.SetBasicAuth(stringValue(extra["user"]), stringValue(secret["password"]))
	response, err := r.SafeHTTP.Do(request)
	if err != nil {
		return nil, nil, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		return nil, nil, fmt.Errorf("clickhouse returned %d", response.StatusCode)
	}
	return nil, nil, nil
}
