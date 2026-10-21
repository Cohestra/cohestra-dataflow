package api

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// memoryDeliveryStore is an in-memory webhookDeliveryStore (TTLs ignored).
type memoryDeliveryStore map[string]string

func (m memoryDeliveryStore) SetNX(_ context.Context, key string, value interface{}, _ time.Duration) *redis.BoolCmd {
	if _, ok := m[key]; ok {
		return redis.NewBoolResult(false, nil)
	}
	m[key] = value.(string)
	return redis.NewBoolResult(true, nil)
}

func (m memoryDeliveryStore) Get(_ context.Context, key string) *redis.StringCmd {
	value, ok := m[key]
	if !ok {
		return redis.NewStringResult("", redis.Nil)
	}
	return redis.NewStringResult(value, nil)
}

func (m memoryDeliveryStore) SetXX(_ context.Context, key string, value interface{}, _ time.Duration) *redis.BoolCmd {
	if _, ok := m[key]; !ok {
		return redis.NewBoolResult(false, nil)
	}
	m[key] = value.(string)
	return redis.NewBoolResult(true, nil)
}

func (m memoryDeliveryStore) Del(_ context.Context, keys ...string) *redis.IntCmd {
	for _, key := range keys {
		delete(m, key)
	}
	return redis.NewIntResult(int64(len(keys)), nil)
}

func deliveryHeader(timestamp, signature, idempotency string) http.Header {
	header := http.Header{}
	header.Set("X-Signature-Timestamp", timestamp)
	header.Set("X-Signature-Sha256", signature)
	if idempotency != "" {
		header.Set("Idempotency-Key", idempotency)
	}
	return header
}

func isConflict(err error) bool {
	var httpErr *HTTPError
	return errors.As(err, &httpErr) && httpErr.Status == http.StatusConflict
}

func TestWebhookClaimRejectsReplayWithChangedIdempotencyKey(t *testing.T) {
	ctx, store := context.Background(), memoryDeliveryStore{}
	claim, _, err := claimWebhookDelivery(ctx, store, "p1", deliveryHeader("100", "abc", "key-a"))
	if err != nil {
		t.Fatal(err)
	}
	claim.complete(ctx, "exec-1")
	// Same signed request, new unsigned header: still a replay.
	if _, _, err := claimWebhookDelivery(ctx, store, "p1", deliveryHeader("100", "abc", "key-b")); !isConflict(err) {
		t.Fatalf("replay with a new Idempotency-Key: err = %v", err)
	}
	if _, _, err := claimWebhookDelivery(ctx, store, "p1", deliveryHeader("100", "abc", "")); !isConflict(err) {
		t.Fatalf("replay without Idempotency-Key: err = %v", err)
	}
}

func TestWebhookClaimRetryReturnsStartedRun(t *testing.T) {
	ctx, store := context.Background(), memoryDeliveryStore{}
	claim, _, _ := claimWebhookDelivery(ctx, store, "p1", deliveryHeader("100", "abc", "key-a"))
	claim.complete(ctx, "exec-1")
	// The sink's retry re-signs with a new timestamp but keeps its key.
	again, existing, err := claimWebhookDelivery(ctx, store, "p1", deliveryHeader("160", "def", "key-a"))
	if err != nil || again != nil || existing != "exec-1" {
		t.Fatalf("retry of a started delivery: claim=%v existing=%q err=%v", again, existing, err)
	}
	// That retry's own signature was released, so it is not stuck either.
	if _, ok := store[sigKeyFor("p1", "160", "def")]; ok {
		t.Fatal("a duplicate delivery must not hold its signature key")
	}
}

func TestWebhookClaimReleasedWhenRunDoesNotStart(t *testing.T) {
	ctx, store := context.Background(), memoryDeliveryStore{}
	claim, _, _ := claimWebhookDelivery(ctx, store, "p1", deliveryHeader("100", "abc", "key-a"))
	claim.release(ctx) // e.g. the Temporal start failed
	retry, existing, err := claimWebhookDelivery(ctx, store, "p1", deliveryHeader("160", "def", "key-a"))
	if err != nil || retry == nil || existing != "" {
		t.Fatalf("retry after a failed start: claim=%v existing=%q err=%v", retry, existing, err)
	}
	// While a delivery is in flight its key is pending, not a run ID.
	if _, _, err := claimWebhookDelivery(ctx, store, "p1", deliveryHeader("170", "ghi", "key-a")); !isConflict(err) {
		t.Fatalf("concurrent delivery with the same key: err = %v", err)
	}
	retry.complete(ctx, "exec-2")
	retry.release(ctx) // deferred release after success is a no-op
	if store[idemKeyFor("p1", "key-a")] != "exec-2" {
		t.Fatalf("completed delivery lost its run: %v", store)
	}
}

func sigKeyFor(pipelineID, timestamp, signature string) string {
	return "dataflow:webhook-seen:" + sha256Hex(pipelineID+":"+timestamp+":"+signature)
}

func idemKeyFor(pipelineID, key string) string {
	return "dataflow:webhook-idem:" + sha256Hex(pipelineID+":"+key)
}
