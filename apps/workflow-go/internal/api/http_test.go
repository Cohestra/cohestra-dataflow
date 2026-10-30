package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestHandleDoesNotExposeInternalError(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	handle(func(http.ResponseWriter, *http.Request) error {
		return errors.New("database password=secret-value")
	})(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "secret-value") {
		t.Fatalf("internal error leaked: %s", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"error":"internal error"`) {
		t.Fatalf("response = %s", recorder.Body.String())
	}
}

func TestRequestIPUsesTrustedProxyHeader(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "10.0.0.2:1234"
	request.Header.Set("X-Forwarded-For", "198.51.100.9")
	request.Header.Set("X-Real-IP", "203.0.113.7")
	if got := requestIP(request); got != "203.0.113.7" {
		t.Fatalf("requestIP = %q", got)
	}
}

func TestRequestIPIgnoresProxyHeaderFromPublicPeer(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "198.51.100.2:1234"
	request.Header.Set("X-Real-IP", "203.0.113.7")
	if got := requestIP(request); got != "198.51.100.2" {
		t.Fatalf("requestIP = %q", got)
	}
}

func TestClientIPTrustsOnlyConfiguredProxies(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "10.0.0.2:1234"
	request.Header.Set("X-Real-IP", "203.0.113.7")
	// Narrowed to the web tier's range, another private peer (any pod in the
	// cluster) can no longer choose its own rate-limit key.
	narrowed := parseTrustedProxies("10.244.1.0/24")
	if got := clientIP(request, narrowed); got != "10.0.0.2" {
		t.Fatalf("untrusted private peer: clientIP = %q", got)
	}
	request.RemoteAddr = "10.244.1.9:1234"
	if got := clientIP(request, narrowed); got != "203.0.113.7" {
		t.Fatalf("trusted proxy: clientIP = %q", got)
	}
	if got := clientIP(request, parseTrustedProxies("none")); got != "10.244.1.9" {
		t.Fatalf("no trusted proxies: clientIP = %q", got)
	}
	request.Header.Set("X-Real-IP", "not-an-ip")
	if got := clientIP(request, narrowed); got != "10.244.1.9" {
		t.Fatalf("malformed header: clientIP = %q", got)
	}
}

func rateLimitedStatus(server *Server, scope, ip string) int {
	handler := server.rateLimit(scope, 3, time.Minute, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	request.RemoteAddr = ip + ":1234"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder.Code
}

func assertLimitsAfterThree(t *testing.T, server *Server, scope string) {
	t.Helper()
	for i := 1; i <= 4; i++ {
		want := http.StatusNoContent
		if i == 4 {
			want = http.StatusTooManyRequests
		}
		if got := rateLimitedStatus(server, scope, "198.51.100.20"); got != want {
			t.Fatalf("request %d: status %d, want %d", i, got, want)
		}
	}
	if got := rateLimitedStatus(server, scope, "198.51.100.21"); got != http.StatusNoContent {
		t.Fatalf("another client shares the limit: status %d", got)
	}
}

func TestRateLimitKeepsLimitingWhenRedisIsDown(t *testing.T) {
	server := &Server{Redis: redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 100 * time.Millisecond})}
	assertLimitsAfterThree(t, server, "test-redis-down-"+uuid.NewString())
}

func TestRateLimitWithRedis(t *testing.T) {
	url := os.Getenv("RATE_LIMIT_TEST_REDIS_URL")
	if url == "" {
		t.Skip("RATE_LIMIT_TEST_REDIS_URL names a disposable Redis")
	}
	options, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{Redis: redis.NewClient(options)}
	scope := "test-redis-" + uuid.NewString()
	assertLimitsAfterThree(t, server, scope)
	keys, err := server.Redis.Keys(context.Background(), "dataflow:rate:"+scope+":*").Result()
	if err != nil || len(keys) != 2 {
		t.Fatalf("keys=%v err=%v", keys, err)
	}
	for _, key := range keys {
		if ttl := server.Redis.TTL(context.Background(), key).Val(); ttl <= 0 || ttl > time.Minute {
			t.Fatalf("%s has TTL %v, want the window", key, ttl)
		}
	}
}
