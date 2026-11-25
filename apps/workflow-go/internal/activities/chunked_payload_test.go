package activities

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/dataflow-poc/workflow-go/internal/connectors"
	"github.com/dataflow-poc/workflow-go/internal/model"
)

// writePart stores records as one inline part, like a source page.
func writePart(t *testing.T, p *Payloads, ids ...int) *model.DataRef {
	t.Helper()
	records := make([]interface{}, 0, len(ids))
	for _, id := range ids {
		records = append(records, map[string]interface{}{"id": float64(id)})
	}
	ref, err := p.Write(context.Background(), records, "tenant", "exec", "source", nil)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func TestChunkedRefFlattensPartsAndSumsSizes(t *testing.T) {
	p := &Payloads{}
	a, b, c := writePart(t, p, 1), writePart(t, p, 2, 3), writePart(t, p, 4)
	inner := ChunkedRef("tenant", []*model.DataRef{b, c})
	ref := ChunkedRef("tenant", []*model.DataRef{a, nil, inner})
	if ref.Type != "chunked" || len(ref.Chunks) != 3 || ref.Chunks[0] != a || ref.Chunks[1] != b || ref.Chunks[2] != c {
		t.Fatalf("parts were not flattened in order: %+v", ref.Chunks)
	}
	if ref.RecordCount != 4 || ref.SizeBytes != a.SizeBytes+b.SizeBytes+c.SizeBytes {
		t.Fatalf("totals: records=%d size=%d", ref.RecordCount, ref.SizeBytes)
	}
}

// A chunked ref under the cap reads back as one array; over the cap, a whole
// read fails with a message that names the parts, but reading part by part
// still works because each part is under the cap.
func TestChunkedRefReadsWholeUnderCapAndPartByPartOverIt(t *testing.T) {
	p := &Payloads{MaxPayloadBytes: 1 << 20}
	ref := ChunkedRef("tenant", []*model.DataRef{writePart(t, p, 1, 2), writePart(t, p, 3)})
	value, err := p.Read(context.Background(), ref, nil)
	want := []interface{}{map[string]interface{}{"id": float64(1)}, map[string]interface{}{"id": float64(2)}, map[string]interface{}{"id": float64(3)}}
	if err != nil || !reflect.DeepEqual(value, want) {
		t.Fatalf("whole read = %v, %v", value, err)
	}

	p.MaxPayloadBytes = int64(ref.Chunks[0].SizeBytes) // each part fits; the total does not
	_, err = p.Read(context.Background(), ref, nil)
	var permanent *connectors.PermanentError
	if !errors.As(err, &permanent) || permanent.Type != PayloadTooLargeError || !strings.Contains(err.Error(), "2 parts") || !strings.Contains(err.Error(), "MAX_PAYLOAD_BYTES") {
		t.Fatalf("over-cap whole read: %v", err)
	}
	var seen []int
	err = p.EachChunk(context.Background(), ref, nil, func(i int, records []interface{}) error {
		seen = append(seen, len(records))
		return nil
	})
	if err != nil || !reflect.DeepEqual(seen, []int{2, 1}) {
		t.Fatalf("part-by-part read = %v, %v", seen, err)
	}
}

func TestEachChunkRejectsAPartThatIsNotARecordArray(t *testing.T) {
	p := &Payloads{}
	scalar, err := p.Write(context.Background(), map[string]interface{}{"not": "an array"}, "tenant", "exec", "source", nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := ChunkedRef("tenant", []*model.DataRef{writePart(t, p, 1), scalar})
	err = p.EachChunk(context.Background(), ref, nil, func(int, []interface{}) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "part 1 is not a record array") {
		t.Fatalf("scalar part: %v", err)
	}
}

// dispatchChunks must call the step once per part with only that part's
// records, and return the outputs as one chunked ref in the original order.
func TestDispatchChunksRunsStepOncePerPart(t *testing.T) {
	p := &Payloads{MaxPayloadBytes: 1 << 20}
	var calls []int
	runtime := &connectors.Runtime{Handlers: map[string]connectors.Handler{
		"transform.map": func(_ context.Context, input interface{}, _ map[string]interface{}, _ connectors.HandlerContext) (interface{}, map[string]interface{}, error) {
			rows := input.([]interface{})
			calls = append(calls, len(rows))
			out := make([]interface{}, 0, len(rows))
			for _, row := range rows {
				out = append(out, map[string]interface{}{"id": row.(map[string]interface{})["id"].(float64) * 10})
			}
			return out, nil, nil
		},
	}}
	a := &Activities{Payloads: p, Runtime: runtime}
	input := ChunkedRef("tenant", []*model.DataRef{writePart(t, p, 1, 2), writePart(t, p, 3), writePart(t, p, 4, 5)})
	p.MaxPayloadBytes = int64(input.Chunks[0].SizeBytes) // the whole input would not fit

	ref, meta, err := a.dispatchChunks(context.Background(), DispatchParams{ActivityType: "transform.map", InputRef: input, TenantID: "tenant", ExecutionID: "exec", NodeID: "map"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []int{2, 1, 2}) || meta["parts"] != 3 {
		t.Fatalf("calls=%v meta=%v", calls, meta)
	}
	if ref.Type != "chunked" || len(ref.Chunks) != 3 || ref.RecordCount != 5 {
		t.Fatalf("output ref = %+v", ref)
	}
	var ids []float64
	p.MaxPayloadBytes = 1 << 20 // "10" is longer than "1": outputs are a little bigger than inputs
	err = p.EachChunk(context.Background(), ref, nil, func(_ int, records []interface{}) error {
		for _, record := range records {
			ids = append(ids, record.(map[string]interface{})["id"].(float64))
		}
		return nil
	})
	if err != nil || !reflect.DeepEqual(ids, []float64{10, 20, 30, 40, 50}) {
		t.Fatalf("output ids = %v, %v", ids, err)
	}
}

// A sink returns no output; its meta counts add up across parts, and a failing
// part stops the run before later parts are written.
func TestDispatchChunksSumsSinkMetaAndStopsOnError(t *testing.T) {
	p := &Payloads{MaxPayloadBytes: 1 << 20}
	written := 0
	runtime := &connectors.Runtime{Handlers: map[string]connectors.Handler{
		"sink.records": func(_ context.Context, input interface{}, _ map[string]interface{}, _ connectors.HandlerContext) (interface{}, map[string]interface{}, error) {
			rows := input.([]interface{})
			if rows[0].(map[string]interface{})["id"].(float64) == 99 {
				return nil, nil, errors.New("sink down")
			}
			written += len(rows)
			return nil, map[string]interface{}{"rowsWritten": len(rows), "table": "t"}, nil
		},
	}}
	a := &Activities{Payloads: p, Runtime: runtime}
	params := DispatchParams{ActivityType: "sink.records", TenantID: "tenant", ExecutionID: "exec", NodeID: "sink"}

	params.InputRef = ChunkedRef("tenant", []*model.DataRef{writePart(t, p, 1, 2), writePart(t, p, 3)})
	ref, meta, err := a.dispatchChunks(context.Background(), params, nil)
	if err != nil || ref != nil || meta["rowsWritten"] != 3 || meta["table"] != "t" || written != 3 {
		t.Fatalf("ref=%v meta=%v err=%v written=%d", ref, meta, err, written)
	}

	written = 0
	params.InputRef = ChunkedRef("tenant", []*model.DataRef{writePart(t, p, 1), writePart(t, p, 99), writePart(t, p, 2)})
	if _, _, err := a.dispatchChunks(context.Background(), params, nil); err == nil || written != 1 {
		t.Fatalf("error part: err=%v written=%d", err, written)
	}
}

func TestStreamsChunksOnlyForPerRecordSteps(t *testing.T) {
	for _, step := range []string{"transform.map", "transform.filter", "transform.formula", "transform.select", "transform.rename", "transform.flatten", "transform.parse", "sink.postgres", "sink.records", "sink.kafka", "sink.webhook"} {
		if !streamsChunks(step) {
			t.Errorf("%s should stream parts", step)
		}
	}
	// These look across records, or write one file or sheet per run, so running
	// them once per part would change the result.
	for _, step := range []string{"transform.dedupe", "transform.contract", "sink.s3", "sink.sftp", "sink.gsheets", "sink.iceberg", "custom.step"} {
		if streamsChunks(step) {
			t.Errorf("%s must read its whole input", step)
		}
	}
}
