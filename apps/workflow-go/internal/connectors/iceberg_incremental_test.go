package connectors

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/apache/iceberg-go"
	iceio "github.com/apache/iceberg-go/io"
	icetable "github.com/apache/iceberg-go/table"
	"github.com/stretchr/testify/require"
)

// memoryCatalog commits table metadata in memory over a local warehouse, so
// incremental reads run against real Iceberg data and manifest files.
type memoryCatalog struct {
	meta     icetable.Metadata
	location string
}

func (c *memoryCatalog) fs(context.Context) (iceio.IO, error) { return iceio.LocalFS{}, nil }

func (c *memoryCatalog) LoadTable(context.Context, icetable.Identifier) (*icetable.Table, error) {
	return icetable.New(icetable.Identifier{"ns", "records"}, c.meta, c.location, c.fs, c), nil
}

func (c *memoryCatalog) CommitTable(_ context.Context, _ icetable.Identifier, requirements []icetable.Requirement, updates []icetable.Update) (icetable.Metadata, string, error) {
	for _, requirement := range requirements {
		if err := requirement.Validate(c.meta); err != nil {
			return nil, "", err
		}
	}
	meta, err := icetable.UpdateTableMetadata(c.meta, updates, c.location)
	if err != nil {
		return nil, "", err
	}
	c.meta = meta
	return meta, c.location, nil
}

func newLocalIcebergTable(t *testing.T) (*memoryCatalog, *icetable.Table) {
	t.Helper()
	dir := t.TempDir()
	schema := iceberg.NewSchema(1,
		iceberg.NestedField{ID: 1, Name: "id", Required: true, Type: iceberg.PrimitiveTypes.Int64},
		iceberg.NestedField{ID: 2, Name: "note", Required: false, Type: iceberg.PrimitiveTypes.String},
	)
	meta, err := icetable.NewMetadata(schema, iceberg.UnpartitionedSpec, icetable.UnsortedSortOrder, dir, iceberg.Properties{})
	require.NoError(t, err)
	cat := &memoryCatalog{meta: meta, location: dir + "/metadata/v1.metadata.json"}
	tbl, err := cat.LoadTable(context.Background(), nil)
	require.NoError(t, err)
	return cat, tbl
}

// appendIDs commits one append snapshot (one data file) holding the given ids.
func appendIDs(t *testing.T, cat *memoryCatalog, ids ...int) *icetable.Table {
	t.Helper()
	tbl, err := cat.LoadTable(context.Background(), nil)
	require.NoError(t, err)
	rows := make([]interface{}, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, map[string]interface{}{"id": float64(id), "note": "row " + strconv.Itoa(id)})
	}
	_, _, err = appendIcebergTable(context.Background(), tbl, rows, HandlerContext{ExecutionID: "append-" + strconv.Itoa(ids[0]), NodeID: "sink"})
	require.NoError(t, err)
	tbl, err = cat.LoadTable(context.Background(), nil)
	require.NoError(t, err)
	return tbl
}

func span(from, to int) []int {
	ids := []int{}
	for id := from; id <= to; id++ {
		ids = append(ids, id)
	}
	return ids
}

// readAll pages a source until it is done, as one workflow run would.
func readAll(t *testing.T, cat *memoryCatalog, cursor map[string]interface{}, pageSize int) ([]int, map[string]interface{}, int) {
	t.Helper()
	ids, pages := []int{}, 0
	for {
		tbl, err := cat.LoadTable(context.Background(), nil)
		require.NoError(t, err)
		result, err := icebergFetchTable(context.Background(), tbl, SourceParams{Config: map[string]interface{}{"pageSize": float64(pageSize)}, Cursor: cursor})
		require.NoError(t, err)
		pages++
		require.LessOrEqual(t, len(result.Records), pageSize)
		for _, record := range result.Records {
			ids = append(ids, int(record.(map[string]interface{})["id"].(float64)))
		}
		cursor = result.NextCursor
		if !result.HasMore {
			return ids, cursor, pages
		}
		require.Less(t, pages, 100, "pagination did not terminate")
	}
}

func TestIcebergIncrementalReadsOnlyNewFiles(t *testing.T) {
	cat, _ := newLocalIcebergTable(t)
	appendIDs(t, cat, span(1, 5)...)
	second := appendIDs(t, cat, span(6, 12)...)

	// First run: every row exactly once, paged across the file boundary.
	ids, cursor, pages := readAll(t, cat, nil, 4)
	sort.Ints(ids)
	require.Equal(t, span(1, 12), ids)
	require.Equal(t, 3, pages)
	require.Equal(t, strconv.FormatInt(second.CurrentSnapshot().SnapshotID, 10), cursor["snapshotId"])
	require.Equal(t, 12, cursor["totalRecords"])

	// Next run after another append reads only the new file.
	third := appendIDs(t, cat, span(13, 15)...)
	ids, cursor, _ = readAll(t, cat, cursor, 4)
	sort.Ints(ids)
	require.Equal(t, span(13, 15), ids)
	require.Equal(t, strconv.FormatInt(third.CurrentSnapshot().SnapshotID, 10), cursor["snapshotId"])

	// Nothing new: no rows, cursor unchanged.
	ids, again, pages := readAll(t, cat, cursor, 4)
	require.Empty(t, ids)
	require.Equal(t, 1, pages)
	require.Equal(t, cursor, again)

	// A new snapshot committed while a run is mid-way is left for the next
	// run: the pending target stays fixed.
	appendIDs(t, cat, span(16, 20)...)
	tbl, err := cat.LoadTable(context.Background(), nil)
	require.NoError(t, err)
	page, err := icebergFetchTable(context.Background(), tbl, SourceParams{Config: map[string]interface{}{"pageSize": float64(2)}, Cursor: cursor})
	require.NoError(t, err)
	require.True(t, page.HasMore)
	appendIDs(t, cat, span(21, 22)...)
	rest, finished, _ := readAll(t, cat, page.NextCursor, 2)
	got := append([]int{}, rest...)
	for _, record := range page.Records {
		got = append(got, int(record.(map[string]interface{})["id"].(float64)))
	}
	sort.Ints(got)
	require.Equal(t, span(16, 20), got)
	ids, _, _ = readAll(t, cat, finished, 10)
	require.Equal(t, span(21, 22), ids)
}

func TestIcebergIncrementalRefusesUnknownBase(t *testing.T) {
	cat, _ := newLocalIcebergTable(t)
	tbl := appendIDs(t, cat, 1, 2)
	_, err := icebergFetchTable(context.Background(), tbl, SourceParams{Cursor: map[string]interface{}{"snapshotId": "12345"}})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "not an ancestor"), err.Error())
}

func TestIcebergIncrementalFinishesLegacyCursor(t *testing.T) {
	cat, _ := newLocalIcebergTable(t)
	appendIDs(t, cat, span(1, 5)...)
	tbl := appendIDs(t, cat, span(6, 8)...)
	snapshot := strconv.FormatInt(tbl.CurrentSnapshot().SnapshotID, 10)
	// A run from before this change stopped mid-snapshot at row 6 of a full scan.
	legacy := map[string]interface{}{"snapshotId": "", "pendingSnapshotId": snapshot, "rowOffset": float64(6), "totalRecords": float64(0)}
	result, err := icebergFetchTable(context.Background(), tbl, SourceParams{Config: map[string]interface{}{"pageSize": float64(10)}, Cursor: legacy})
	require.NoError(t, err)
	require.Len(t, result.Records, 2)
	require.False(t, result.HasMore)
	require.Equal(t, snapshot, result.NextCursor["snapshotId"])
}
