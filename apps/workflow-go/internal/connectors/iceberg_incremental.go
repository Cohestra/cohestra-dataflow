package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"sync"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	icetable "github.com/apache/iceberg-go/table"
)

// icebergFetchTable reads the rows a table gained since the cursor's snapshot.
// It plans the target snapshot's files from manifest metadata, drops the files
// the cursor snapshot already had, and reads only the new ones, so each page
// costs one data file's worth of reading instead of a scan from row 0.
//
// Cursor: {snapshotId, totalRecords} once a snapshot is fully read, and
// {snapshotId (base), pendingSnapshotId, fileIndex, fileRowOffset} while one
// is being paged. Files are sorted by path, so positions stay valid across
// pages; append snapshots never remove files, so the set is stable too.
func icebergFetchTable(ctx context.Context, tbl *icetable.Table, p SourceParams) (SourceResult, error) {
	if _, legacy := p.Cursor["rowOffset"]; legacy {
		if _, current := p.Cursor["fileIndex"]; !current {
			return legacyIcebergPage(ctx, tbl, p)
		}
	}
	current := tbl.CurrentSnapshot()
	if current == nil {
		return SourceResult{Records: []interface{}{}, NextCursor: p.Cursor}, nil
	}
	base := stringValue(p.Cursor["snapshotId"])
	target := stringValue(p.Cursor["pendingSnapshotId"])
	if target == "" {
		target = strconv.FormatInt(current.SnapshotID, 10)
	}
	if target == base {
		return SourceResult{Records: []interface{}{}, NextCursor: p.Cursor, HasMore: false}, nil
	}
	targetID, err := strconv.ParseInt(target, 10, 64)
	if err != nil {
		return SourceResult{}, fmt.Errorf("iceberg.fetch: invalid cursor snapshot %q", target)
	}
	targetSnapshot := tbl.Metadata().SnapshotByID(targetID)
	if targetSnapshot == nil {
		return SourceResult{}, fmt.Errorf("iceberg.fetch: snapshot %s no longer exists; run a backfill", target)
	}

	newFiles, err := icebergFilesSince(ctx, tbl, base, targetID)
	if err != nil {
		return SourceResult{}, err
	}
	pageSize := icebergPageSize(p)
	fileIndex := int(firstNumber(p.Cursor["fileIndex"], 0))
	skip := int64(firstNumber(p.Cursor["fileRowOffset"], 0))
	if p.Cursor["pendingSnapshotId"] == nil {
		fileIndex, skip = 0, 0
	}

	records := []interface{}{}
	for fileIndex < len(newFiles) && len(records) < pageSize {
		read, err := readIcebergFileRows(ctx, tbl, newFiles[fileIndex], skip, pageSize-len(records))
		if err != nil {
			return SourceResult{}, err
		}
		records = append(records, read.rows...)
		if read.exhausted {
			fileIndex, skip = fileIndex+1, 0
		} else {
			skip += int64(len(read.rows))
		}
	}

	if fileIndex >= len(newFiles) {
		totalRecords, _ := strconv.Atoi(targetSnapshot.Summary.Properties["total-records"])
		// totalRecords keeps the cursor readable by the legacy row-offset reader.
		return SourceResult{Records: records, NextCursor: map[string]interface{}{"snapshotId": target, "totalRecords": totalRecords}}, nil
	}
	next := map[string]interface{}{"snapshotId": p.Cursor["snapshotId"], "pendingSnapshotId": target, "fileIndex": fileIndex, "fileRowOffset": skip}
	return SourceResult{Records: records, NextCursor: next, HasMore: true}, nil
}

func icebergPageSize(p SourceParams) int {
	var ingestion interface{}
	if p.Ingestion != nil {
		ingestion = p.Ingestion.PageSize
	}
	pageSize := int(firstNumber(ingestion, p.Config["pageSize"], 1000))
	return min(max(pageSize, 1), 10000)
}

// icebergFilesSince returns the target snapshot's data files that the base
// snapshot did not have (all of them when there is no base), sorted by path.
// Every snapshot after the base must be an append: overwrites and deletes
// change rows that were already read, which an incremental read cannot express.
func icebergFilesSince(ctx context.Context, tbl *icetable.Table, base string, targetID int64) ([]icetable.FileScanTask, error) {
	// Snapshots are immutable, so one run's pages can reuse the plan instead
	// of re-reading every manifest per page.
	key := fmt.Sprintf("%s/%s/%d", tbl.Metadata().TableUUID(), base, targetID)
	if tasks, ok := icebergPlans.get(key); ok {
		return tasks, nil
	}
	known := map[string]bool{}
	if base != "" {
		baseID, err := strconv.ParseInt(base, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("iceberg.fetch: invalid cursor snapshot %q", base)
		}
		between, found := icetable.AncestorsBetween(targetID, baseID, tbl.Metadata().SnapshotByID)
		if !found {
			return nil, fmt.Errorf("iceberg.fetch: snapshot %s is not an ancestor of %d (expired or rolled back); run a backfill", base, targetID)
		}
		for _, snapshot := range between {
			if snapshot.Summary != nil && snapshot.Summary.Operation != icetable.OpAppend {
				return nil, fmt.Errorf("iceberg.fetch: incremental mode supports append snapshots; snapshot %d is %q, run a backfill", snapshot.SnapshotID, snapshot.Summary.Operation)
			}
		}
		baseFiles, err := tbl.Scan(icetable.WithSnapshotID(baseID)).PlanFiles(ctx)
		if err != nil {
			return nil, err
		}
		for _, task := range baseFiles {
			known[task.File.FilePath()] = true
		}
	}
	tasks, err := tbl.Scan(icetable.WithSnapshotID(targetID)).PlanFiles(ctx)
	if err != nil {
		return nil, err
	}
	fresh := tasks[:0]
	for _, task := range tasks {
		if !known[task.File.FilePath()] {
			fresh = append(fresh, task)
		}
	}
	sort.Slice(fresh, func(i, j int) bool { return fresh[i].File.FilePath() < fresh[j].File.FilePath() })
	icebergPlans.put(key, fresh)
	return fresh, nil
}

// icebergPlanCache keeps the last few file plans per worker process.
type icebergPlanCache struct {
	mu    sync.Mutex
	order []string
	plans map[string][]icetable.FileScanTask
}

const icebergPlanCacheSize = 32

var icebergPlans = &icebergPlanCache{plans: map[string][]icetable.FileScanTask{}}

func (c *icebergPlanCache) get(key string) ([]icetable.FileScanTask, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tasks, ok := c.plans[key]
	return tasks, ok
}

func (c *icebergPlanCache) put(key string, tasks []icetable.FileScanTask) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.plans[key]; ok {
		return
	}
	if len(c.order) >= icebergPlanCacheSize {
		delete(c.plans, c.order[0])
		c.order = c.order[1:]
	}
	c.order = append(c.order, key)
	c.plans[key] = tasks
}

type icebergFileRead struct {
	rows      []interface{}
	exhausted bool
}

// readIcebergFileRows returns up to limit rows of one data file after skipping
// the first skip rows. Skipped batches are counted, not decoded.
func readIcebergFileRows(ctx context.Context, tbl *icetable.Table, task icetable.FileScanTask, skip int64, limit int) (icebergFileRead, error) {
	_, batches, err := tbl.Scan().ReadTasks(ctx, []icetable.FileScanTask{task})
	if err != nil {
		return icebergFileRead{}, err
	}
	out := icebergFileRead{exhausted: true}
	for batch, batchErr := range batches {
		if batchErr != nil {
			return icebergFileRead{}, batchErr
		}
		if len(out.rows) >= limit {
			// The page is full and the file still has rows.
			batch.Release()
			out.exhausted = false
			break
		}
		n := batch.NumRows()
		if skip >= n {
			skip -= n
			batch.Release()
			continue
		}
		end := min(n, skip+int64(limit-len(out.rows)))
		rows, err := icebergBatchRows(batch.NewSlice(skip, end))
		batch.Release()
		if err != nil {
			return icebergFileRead{}, err
		}
		out.rows = append(out.rows, rows...)
		if end < n {
			out.exhausted = false
			break
		}
		skip = 0
	}
	return out, nil
}

func icebergBatchRows(batch arrow.RecordBatch) ([]interface{}, error) {
	defer batch.Release()
	encoded, err := array.RecordToStructArray(batch).MarshalJSON()
	if err != nil {
		return nil, err
	}
	var values []interface{}
	return values, json.Unmarshal(encoded, &values)
}
