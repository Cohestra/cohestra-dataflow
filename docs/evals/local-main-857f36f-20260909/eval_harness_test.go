package api

import (
 "context"
 "encoding/json"
 "fmt"
 "io"
 "net/http"
 "os"
 "os/signal"
 "strings"
 "syscall"
 "testing"
 "time"

 "github.com/dataflow-poc/workflow-go/internal/database"
 "github.com/dataflow-poc/workflow-go/internal/model"
)

type evalTransport struct { file *os.File }
func (e evalTransport) RoundTrip(r *http.Request) (*http.Response, error) {
 body, err := io.ReadAll(r.Body)
 if err != nil { return nil, err }
 r.Body = io.NopCloser(strings.NewReader(string(body)))
 started := time.Now()
 response, err := http.DefaultTransport.RoundTrip(r)
 record := map[string]interface{}{"request": json.RawMessage(body), "startedAt": started.UTC().Format(time.RFC3339Nano)}
 if err != nil { record["error"] = err.Error() } else {
  content, readErr := io.ReadAll(response.Body)
  response.Body.Close()
  response.Body = io.NopCloser(strings.NewReader(string(content)))
  record["httpStatus"] = response.StatusCode
  if json.Valid(content) { record["response"] = json.RawMessage(content) } else { record["responseText"] = string(content) }
  if readErr != nil { record["readError"] = readErr.Error() }
 }
 record["elapsedMs"] = time.Since(started).Milliseconds()
 if err := json.NewEncoder(e.file).Encode(record); err != nil { panic(err) }
 return response, err
}

func TestEvalHTTPHarness(t *testing.T) {
 ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
 defer stop()
 db, err := database.Open(ctx, "postgres://eval:synthetic-local-eval@127.0.0.1:15432/cohestra_eval?sslmode=disable")
 if err != nil { t.Fatal(err) }
 defer db.Close()
 _, err = db.Pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS connector_instances (id uuid PRIMARY KEY, kind text NOT NULL, provider text NOT NULL, provider_account_email text NOT NULL); TRUNCATE connector_instances`)
 if err != nil { t.Fatal(err) }
 var suite struct { Fixtures struct { Connectors []struct { Provider, Kind, DisplayName string } } }
 data, err := os.ReadFile("/private/tmp/cohestra-plan-20260909/tests/ai-evals/cases/v1.json")
 if err != nil { t.Fatal(err) }
 if err := json.Unmarshal(data, &suite); err != nil { t.Fatal(err) }
 if len(suite.Fixtures.Connectors) != 12 { t.Fatal("unexpected fixture count") }
 for i, fixture := range suite.Fixtures.Connectors {
  _, err := db.Pool.Exec(ctx, `INSERT INTO connector_instances VALUES ($1,$2,$3,$4)`, fmt.Sprintf("00000000-0000-4000-8000-%012d",i+1), fixture.Kind, fixture.Provider, fixture.DisplayName)
  if err != nil { t.Fatal(err) }
 }
 logPath := os.Getenv("EVAL_OLLAMA_LOG")
 if logPath == "" { t.Fatal("EVAL_OLLAMA_LOG required") }
 log, err := os.OpenFile(logPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
 if err != nil { t.Fatal(err) }
 defer log.Close()
 s := &Server{DB: db, HTTP: &http.Client{Transport: evalTransport{log}}}
 mux := http.NewServeMux()
 s.registerAI(mux)
 mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
 // This isolated harness tests exact AI handlers; authentication, background
 // workers and connector execution are outside the model accuracy evaluation.
 handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  mux.ServeHTTP(w, withTenant(r, model.TenantContext{TenantID:"00000000-0000-4000-8000-000000000000", Role:"owner", EmailVerified:true}))
 })
 server := &http.Server{Addr:"127.0.0.1:14000", Handler:handler, ReadHeaderTimeout:10*time.Second}
 go func(){ <-ctx.Done(); server.Close() }()
 t.Logf("isolated exact-main AI handlers, model=%s, seeded=%d", os.Getenv("OLLAMA_MODEL"), len(suite.Fixtures.Connectors))
 if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed { t.Fatal(err) }
}
