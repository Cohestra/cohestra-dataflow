package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/dataflow-poc/workflow-go/internal/connectors"
	"github.com/dataflow-poc/workflow-go/internal/model"
	"github.com/dataflow-poc/workflow-go/internal/security"
	"github.com/jackc/pgx/v5"
)

var credentialProviders = map[string]bool{"postgres": true, "mysql": true, "mongodb": true, "clickhouse": true, "s3": true, "sftp": true, "snowflake": true, "iceberg": true, "kafka": true, "http": true}
var googleScopes = []string{"openid", "email", "profile", "https://www.googleapis.com/auth/spreadsheets", "https://www.googleapis.com/auth/drive.readonly"}
var microsoftScopes = []string{"offline_access", "User.Read", "Files.Read", "Sites.Read.All"}

func (s *Server) registerConnectors(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/connectors/catalog", handle(s.connectorCatalog))
	mux.HandleFunc("GET /api/connectors", handle(s.connectorList))
	mux.HandleFunc("POST /api/connectors", handle(s.connectorCreate))
	mux.HandleFunc("DELETE /api/connectors/{connectionId}", handle(s.connectorDelete))
	mux.HandleFunc("POST /api/connectors/{connectionId}/test", handle(s.connectorTest))
	mux.HandleFunc("POST /api/connectors/{connectionId}/refresh", handle(s.connectorRefresh))
	mux.Handle("PUT /api/connectors/{connectionId}/cdc", s.requireFeature("realtime", handle(s.connectorCDCEnable)))
	mux.HandleFunc("GET /api/connectors/{connectionId}/cdc", handle(s.connectorCDCGet))
	mux.HandleFunc("DELETE /api/connectors/{connectionId}/cdc", handle(s.connectorCDCDelete))
	mux.HandleFunc("GET /api/connectors/google/auth", handle(s.googleConnectorStart))
	mux.HandleFunc("GET /api/connectors/google/callback", handle(s.googleConnectorCallback))
	mux.HandleFunc("GET /api/connectors/google/spreadsheets", handle(s.googleSpreadsheets))
	mux.HandleFunc("GET /api/connectors/google/spreadsheets/{id}/sheets", handle(s.googleSheets))
	mux.HandleFunc("GET /api/connectors/google/spreadsheets/{id}/sheets/{name}/preview", handle(s.googleSheetPreview))
	mux.HandleFunc("GET /api/connectors/google/drive/folders", handle(s.googleFolders))
	mux.HandleFunc("GET /api/connectors/google/drive/files/{id}/preview", handle(s.googleFilePreview))
	mux.HandleFunc("GET /api/connectors/microsoft/auth", handle(s.microsoftConnectorStart))
	mux.HandleFunc("GET /api/connectors/microsoft/callback", handle(s.microsoftConnectorCallback))
	mux.HandleFunc("GET /api/connectors/microsoft/drives", handle(s.microsoftDrives))
	mux.HandleFunc("GET /api/connectors/microsoft/drives/{driveId}/items", handle(s.microsoftItems))
	mux.HandleFunc("GET /api/connectors/microsoft/workbooks/{itemId}/sheets", handle(s.microsoftSheets))
	mux.HandleFunc("GET /api/connectors/microsoft/workbooks/{itemId}/sheets/{name}/preview", handle(s.microsoftPreview))
	mux.HandleFunc("POST /api/connectors/zendesk/auth", handle(s.zendeskStart))
	mux.HandleFunc("GET /api/connectors/zendesk/callback", handle(s.zendeskCallback))
	mux.HandleFunc("GET /api/connectors/zendesk/resources", handle(s.zendeskResources))
}

func (s *Server) connectorCatalog(w http.ResponseWriter, r *http.Request) error {
	jsonResponse(w, http.StatusOK, map[string]interface{}{"catalog": s.Connectors.Catalog()})
	return nil
}
func (s *Server) connectorList(w http.ResponseWriter, r *http.Request) error {
	tenant := tenantFrom(r)
	rows, err := tenantQueryRows(r.Context(), s.DB, tenant.TenantID, `SELECT id,kind,provider,provider_account_email,scopes,expires_at,extra,created_at FROM connector_instances ORDER BY kind,provider,created_at DESC`)
	if err != nil {
		return err
	}
	out := make([]map[string]interface{}, 0, len(rows))
	for _, row := range rows {
		extra, _ := row["extra"].(map[string]interface{})
		out = append(out, map[string]interface{}{"id": row["id"], "kind": row["kind"], "provider": row["provider"], "name": row["provider_account_email"], "email": row["provider_account_email"], "subdomain": extra["subdomain"], "host": extra["host"], "brokers": extra["brokers"], "baseUrl": extra["baseUrl"], "cdc": extra["cdc"], "expires_at": row["expires_at"], "connected_at": row["created_at"]})
	}
	jsonResponse(w, http.StatusOK, out)
	return nil
}
func (s *Server) connection(r *http.Request, id string) (map[string]interface{}, error) {
	tenant := tenantFrom(r)
	rows, err := tenantQueryRows(r.Context(), s.DB, tenant.TenantID, `SELECT * FROM connector_instances WHERE id=$1`, id)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}

func validateCredential(provider string, config, secret map[string]interface{}) error {
	if !credentialProviders[provider] {
		return fmt.Errorf("unsupported credential provider %q", provider)
	}
	required := func(source map[string]interface{}, fields ...string) error {
		missing := []string{}
		for _, field := range fields {
			// A missing field is nil, which stringValue renders as "<nil>".
			if value := source[field]; value == nil || strings.TrimSpace(stringValue(value)) == "" {
				missing = append(missing, field)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("%s requires %s", provider, strings.Join(missing, ", "))
		}
		return nil
	}
	switch provider {
	case "postgres", "mysql":
		if err := required(config, "host", "database", "user"); err != nil {
			return err
		}
		return required(secret, "password")
	case "mongodb":
		if err := required(config, "host", "database"); err != nil {
			return err
		}
		if config["user"] != nil {
			return required(secret, "password")
		}
	case "clickhouse":
		return required(config, "url")
	case "s3":
		if err := required(config, "region"); err != nil {
			return err
		}
		return required(secret, "accessKeyId", "secretAccessKey")
	case "sftp":
		if err := required(config, "host", "user", "hostKey"); err != nil {
			return err
		}
		if secret["password"] == nil && secret["privateKey"] == nil {
			return fmt.Errorf("sftp requires password or privateKey")
		}
	case "snowflake":
		if err := required(config, "account", "user", "database", "warehouse"); err != nil {
			return err
		}
		return required(secret, "password")
	case "iceberg":
		return required(config, "url", "warehouse")
	case "kafka":
		if err := required(config, "brokers"); err != nil {
			return err
		}
		// Same rules the worker applies when it connects, so a bad login fails here.
		// (stringValue would turn a missing field into "<nil>".)
		mechanism, _ := config["saslMechanism"].(string)
		username, _ := secret["username"].(string)
		password, _ := secret["password"].(string)
		_, err := connectors.KafkaSASLMechanism(mechanism, boolValue(config["tls"]), username, password)
		return err
	case "http":
		return required(config, "baseUrl")
	}
	return nil
}
func (s *Server) connectorCreate(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Provider, Name string
		Config, Secret map[string]interface{}
	}
	if !decodeJSON(w, r, &body) {
		return nil
	}
	if body.Provider == "" || body.Name == "" {
		return badRequest(ErrInvalidRequest, "provider and name are required")
	}
	if err := validateCredential(body.Provider, body.Config, body.Secret); err != nil {
		return badRequest(ErrInvalidRequest, err.Error())
	}
	features, _ := s.paidFeatures(r)
	if body.Provider == "kafka" && !features["realtime"] {
		return &HTTPError{Status: http.StatusPaymentRequired, Message: "realtime is not enabled for this workspace"}
	}
	if map[string]bool{"sftp": true, "snowflake": true, "iceberg": true}[body.Provider] && !features["advancedConnectors"] {
		return &HTTPError{Status: http.StatusPaymentRequired, Message: "advancedConnectors is not enabled for this workspace"}
	}
	secretJSON, _ := json.Marshal(body.Secret)
	encrypted, err := s.encryptToken(string(secretJSON))
	if err != nil {
		return err
	}
	tenant := tenantFrom(r)
	var id string
	err = s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `INSERT INTO connector_instances (tenant_id,user_id,kind,provider,provider_account_email,secret,extra) VALUES ($1,$2,'credential',$3,$4,$5,$6) RETURNING id`, tenant.TenantID, tenant.UserID, body.Provider, body.Name, encrypted, body.Config).Scan(&id)
	})
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key value") {
			return badRequest(ErrInvalidRequest, "A connector with this name already exists")
		}
		return fmt.Errorf("failed to insert connector: %w", err)
	}
	s.audit(r.Context(), tenant, "connector.created", id, map[string]string{"provider": body.Provider, "kind": "credential"}, r)
	jsonResponse(w, http.StatusOK, map[string]string{"id": id})
	return nil
}
func (s *Server) connectorDelete(w http.ResponseWriter, r *http.Request) error {
	tenant := tenantFrom(r)
	changed := false
	err := s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		cmd, err := tx.Exec(r.Context(), `DELETE FROM connector_instances WHERE id=$1`, r.PathValue("connectionId"))
		changed = cmd.RowsAffected() > 0
		return err
	})
	if err != nil {
		return err
	}
	if !changed {
		return notFound(ErrNotFound, "not found")
	}
	jsonResponse(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}
func (s *Server) connectorTest(w http.ResponseWriter, r *http.Request) error {
	row, err := s.connection(r, r.PathValue("connectionId"))
	if err != nil {
		return err
	}
	if row == nil {
		return notFound(ErrNotFound, "not found")
	}
	if row["kind"] == "oauth" {
		if _, err = s.liveToken(r, row); err != nil {
			jsonResponse(w, http.StatusOK, map[string]interface{}{"ok": false, "message": err.Error()})
			return nil
		}
		jsonResponse(w, http.StatusOK, map[string]interface{}{"ok": true, "message": "token OK"})
		return nil
	}
	extra, _ := row["extra"].(map[string]interface{})
	if row["provider"] == "http" {
		if _, err := security.ValidateURL(stringValue(extra["baseUrl"])); err != nil {
			jsonResponse(w, http.StatusOK, map[string]interface{}{"ok": false, "message": err.Error()})
			return nil
		}
		request, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, stringValue(extra["baseUrl"]), nil)
		response, err := s.SafeHTTP.Do(request)
		if err != nil {
			jsonResponse(w, http.StatusOK, map[string]interface{}{"ok": false, "message": err.Error()})
			return nil
		}
		response.Body.Close()
		ok := response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices
		jsonResponse(w, http.StatusOK, map[string]interface{}{"ok": ok, "message": fmt.Sprintf("HTTP %d", response.StatusCode)})
		return nil
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"ok": true, "message": "credentials validated"})
	return nil
}

func cdcName(tenantID, id string) string {
	sum := sha256.Sum256([]byte(tenantID + ":" + id))
	return "df_" + hex.EncodeToString(sum[:])[:24]
}
func (s *Server) connectorCDCEnable(w http.ResponseWriter, r *http.Request) error {
	row, err := s.connection(r, r.PathValue("connectionId"))
	if err != nil {
		return err
	}
	if row == nil {
		return notFound(ErrNotFound, "not found")
	}
	var body struct {
		Resources []string `json:"resources"`
	}
	if !decodeJSON(w, r, &body) {
		return nil
	}
	if len(body.Resources) == 0 || len(body.Resources) > 100 {
		return badRequest(ErrInvalidRequest, "at least one and at most 100 tables or collections are required")
	}
	valid := regexp.MustCompile(`^[A-Za-z0-9_$-]+(?:\.[A-Za-z0-9_$-]+){1,2}$`)
	for _, resource := range body.Resources {
		if !valid.MatchString(resource) {
			return badRequest(ErrInvalidRequest, "invalid table or collection "+resource)
		}
	}
	provider := stringValue(row["provider"])
	if !map[string]bool{"postgres": true, "mysql": true, "mongodb": true}[provider] {
		return badRequest(ErrInvalidRequest, "CDC is not supported for provider "+provider)
	}
	name := cdcName(tenantFrom(r).TenantID, stringValue(row["id"]))
	config := map[string]string{"name": name, "connector.class": map[string]string{"postgres": "io.debezium.connector.postgresql.PostgresConnector", "mysql": "io.debezium.connector.mysql.MySqlConnector", "mongodb": "io.debezium.connector.mongodb.MongoDbConnector"}[provider], "topic.prefix": name, "tasks.max": "1"}
	if err = s.connectRequest(r, http.MethodPut, "/connectors/"+url.PathEscape(name)+"/config", config, nil); err != nil {
		return &HTTPError{Status: http.StatusServiceUnavailable, Message: err.Error()}
	}
	cdc := map[string]interface{}{"enabled": true, "resources": body.Resources, "topicPrefix": name, "connectorName": name}
	tenant := tenantFrom(r)
	err = s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(r.Context(), `UPDATE connector_instances SET extra=coalesce(extra,'{}'::jsonb)||jsonb_build_object('cdc',$1::jsonb) WHERE id=$2`, cdc, row["id"])
		return err
	})
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, cdc)
	return nil
}
func (s *Server) connectRequest(r *http.Request, method, path string, body, target interface{}) error {
	payload := []byte(nil)
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	request, _ := http.NewRequestWithContext(r.Context(), method, env("KAFKA_CONNECT_URL", "http://kafka-connect:8083")+path, bytes.NewReader(payload))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if target == nil {
		target = &map[string]interface{}{}
	}
	return s.doJSON(request, target)
}
func (s *Server) connectorCDCGet(w http.ResponseWriter, r *http.Request) error {
	row, err := s.connection(r, r.PathValue("connectionId"))
	if err != nil {
		return err
	}
	if row == nil {
		return notFound(ErrNotFound, "not found")
	}
	extra, _ := row["extra"].(map[string]interface{})
	cdc, _ := extra["cdc"].(map[string]interface{})
	if !boolValue(cdc["enabled"]) {
		jsonResponse(w, http.StatusOK, map[string]bool{"enabled": false})
		return nil
	}
	status := map[string]interface{}{}
	if err = s.connectRequest(r, http.MethodGet, "/connectors/"+url.PathEscape(stringValue(cdc["connectorName"]))+"/status", nil, &status); err != nil {
		cdc["state"] = "UNAVAILABLE"
		cdc["error"] = err.Error()
	} else {
		connector, _ := status["connector"].(map[string]interface{})
		cdc["state"] = connector["state"]
		cdc["tasks"] = status["tasks"]
	}
	jsonResponse(w, http.StatusOK, cdc)
	return nil
}
func (s *Server) connectorCDCDelete(w http.ResponseWriter, r *http.Request) error {
	row, err := s.connection(r, r.PathValue("connectionId"))
	if err != nil {
		return err
	}
	if row == nil {
		return notFound(ErrNotFound, "not found")
	}
	extra, _ := row["extra"].(map[string]interface{})
	cdc, _ := extra["cdc"].(map[string]interface{})
	if boolValue(cdc["enabled"]) {
		_ = s.connectRequest(r, http.MethodDelete, "/connectors/"+url.PathEscape(stringValue(cdc["connectorName"])), nil, nil)
	}
	tenant := tenantFrom(r)
	err = s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(r.Context(), `UPDATE connector_instances SET extra=coalesce(extra,'{}'::jsonb)-'cdc' WHERE id=$1`, row["id"])
		return err
	})
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}

type oauthState struct {
	TenantID, UserID, Provider string
	Extra                      map[string]interface{} `json:"extra,omitempty"`
}

func (s *Server) mintOAuthState(r *http.Request, provider string, extra map[string]interface{}) (string, error) {
	nonce := randomToken()
	tenant := tenantFrom(r)
	body, _ := json.Marshal(oauthState{TenantID: tenant.TenantID, UserID: tenant.UserID, Provider: provider, Extra: extra})
	return nonce, s.Redis.Set(r.Context(), "oauth:state:"+nonce, body, 5*time.Minute).Err()
}
func (s *Server) consumeOAuthState(r *http.Request, state, provider string) (oauthState, error) {
	key := "oauth:state:" + state
	body, err := s.Redis.GetDel(r.Context(), key).Bytes()
	if err != nil {
		return oauthState{}, fmt.Errorf("invalid state")
	}
	var value oauthState
	if json.Unmarshal(body, &value) != nil || value.Provider != provider || value.TenantID == "" || value.UserID == "" {
		return oauthState{}, fmt.Errorf("invalid state")
	}
	return value, nil
}
func oauthURL(base string, values url.Values) string { return base + "?" + values.Encode() }
func (s *Server) googleConnectorStart(w http.ResponseWriter, r *http.Request) error {
	if os.Getenv("GOOGLE_CLIENT_ID") == "" || os.Getenv("GOOGLE_CLIENT_SECRET") == "" {
		return &HTTPError{Status: http.StatusServiceUnavailable, Message: "Google OAuth is not configured"}
	}
	state, err := s.mintOAuthState(r, "google", nil)
	if err != nil {
		return err
	}
	values := url.Values{"client_id": {os.Getenv("GOOGLE_CLIENT_ID")}, "redirect_uri": {s.Config.AppURL + "/api/connectors/google/callback"}, "response_type": {"code"}, "access_type": {"offline"}, "prompt": {"consent"}, "scope": {strings.Join(googleScopes, " ")}, "state": {state}}
	jsonResponse(w, http.StatusOK, map[string]string{"url": oauthURL("https://accounts.google.com/o/oauth2/v2/auth", values)})
	return nil
}
func (s *Server) googleConnectorCallback(w http.ResponseWriter, r *http.Request) error {
	state, err := s.consumeOAuthState(r, r.URL.Query().Get("state"), "google")
	if err != nil {
		return badRequest(ErrInvalidRequest, "invalid state")
	}
	values := url.Values{"code": {r.URL.Query().Get("code")}, "client_id": {os.Getenv("GOOGLE_CLIENT_ID")}, "client_secret": {os.Getenv("GOOGLE_CLIENT_SECRET")}, "redirect_uri": {s.Config.AppURL + "/api/connectors/google/callback"}, "grant_type": {"authorization_code"}}
	var token map[string]interface{}
	if err = s.postFormJSON(r, "https://oauth2.googleapis.com/token", values, &token); err != nil {
		return err
	}
	access, refresh := stringValue(token["access_token"]), stringValue(token["refresh_token"])
	if access == "" || refresh == "" {
		return badRequest(ErrInvalidRequest, "Google did not return a refresh token. Revoke the existing grant and retry.")
	}
	request, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, "https://www.googleapis.com/oauth2/v2/userinfo", nil)
	request.Header.Set("Authorization", "Bearer "+access)
	var me map[string]interface{}
	if err = s.doJSON(request, &me); err != nil {
		return err
	}
	id, err := s.upsertOAuth(r, state, "google", stringValue(me["email"]), googleScopes, access, refresh, time.Now().Add(time.Duration(numberInt(token["expires_in"]))*time.Second), nil)
	if err != nil {
		return err
	}
	s.audit(r.Context(), model.TenantContext{TenantID: state.TenantID, UserID: state.UserID}, "connector.connected", id, map[string]interface{}{"provider": "google", "email": me["email"]}, r)
	http.Redirect(w, r, s.Config.AppURL+"/connectors?connected=google", http.StatusFound)
	return nil
}
func (s *Server) microsoftConnectorStart(w http.ResponseWriter, r *http.Request) error {
	state, err := s.mintOAuthState(r, "microsoft", nil)
	if err != nil {
		return err
	}
	tenant := env("AZURE_TENANT_ID", "common")
	values := url.Values{"client_id": {os.Getenv("AZURE_CLIENT_ID")}, "redirect_uri": {s.Config.AppURL + "/api/connectors/microsoft/callback"}, "response_type": {"code"}, "response_mode": {"query"}, "scope": {strings.Join(microsoftScopes, " ")}, "prompt": {"consent"}, "state": {state}}
	jsonResponse(w, http.StatusOK, map[string]string{"url": oauthURL("https://login.microsoftonline.com/"+tenant+"/oauth2/v2.0/authorize", values)})
	return nil
}
func (s *Server) microsoftConnectorCallback(w http.ResponseWriter, r *http.Request) error {
	state, err := s.consumeOAuthState(r, r.URL.Query().Get("state"), "microsoft")
	if err != nil {
		return badRequest(ErrInvalidRequest, "invalid state")
	}
	tenant := env("AZURE_TENANT_ID", "common")
	values := url.Values{"client_id": {os.Getenv("AZURE_CLIENT_ID")}, "client_secret": {os.Getenv("AZURE_CLIENT_SECRET")}, "code": {r.URL.Query().Get("code")}, "redirect_uri": {s.Config.AppURL + "/api/connectors/microsoft/callback"}, "grant_type": {"authorization_code"}, "scope": {strings.Join(microsoftScopes, " ")}}
	var token map[string]interface{}
	if err = s.postFormJSON(r, "https://login.microsoftonline.com/"+tenant+"/oauth2/v2.0/token", values, &token); err != nil {
		return err
	}
	access, refresh := stringValue(token["access_token"]), stringValue(token["refresh_token"])
	request, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, "https://graph.microsoft.com/v1.0/me", nil)
	request.Header.Set("Authorization", "Bearer "+access)
	var me map[string]interface{}
	if err = s.doJSON(request, &me); err != nil {
		return err
	}
	email := stringValue(me["mail"])
	if email == "" {
		email = stringValue(me["userPrincipalName"])
	}
	id, err := s.upsertOAuth(r, state, "microsoft", email, microsoftScopes, access, refresh, time.Now().Add(time.Duration(numberInt(token["expires_in"]))*time.Second), nil)
	if err != nil {
		return err
	}
	s.audit(r.Context(), model.TenantContext{TenantID: state.TenantID, UserID: state.UserID}, "connector.connected", id, map[string]string{"provider": "microsoft", "email": email}, r)
	http.Redirect(w, r, s.Config.AppURL+"/connectors?connected=microsoft", http.StatusFound)
	return nil
}
func (s *Server) zendeskStart(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Subdomain string `json:"subdomain"`
	}
	if !decodeJSON(w, r, &body) {
		return nil
	}
	body.Subdomain = strings.ToLower(strings.TrimSpace(body.Subdomain))
	if !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(body.Subdomain) {
		return badRequest(ErrInvalidRequest, "invalid subdomain")
	}
	state, err := s.mintOAuthState(r, "zendesk", map[string]interface{}{"subdomain": body.Subdomain})
	if err != nil {
		return err
	}
	values := url.Values{"response_type": {"code"}, "client_id": {os.Getenv("ZENDESK_OAUTH_CLIENT_ID")}, "redirect_uri": {s.Config.AppURL + "/api/connectors/zendesk/callback"}, "scope": {"read"}, "state": {state}}
	jsonResponse(w, http.StatusOK, map[string]string{"url": oauthURL("https://"+body.Subdomain+".zendesk.com/oauth/authorizations/new", values)})
	return nil
}
func (s *Server) zendeskCallback(w http.ResponseWriter, r *http.Request) error {
	state, err := s.consumeOAuthState(r, r.URL.Query().Get("state"), "zendesk")
	if err != nil {
		return badRequest(ErrInvalidRequest, "invalid state")
	}
	subdomain := stringValue(state.Extra["subdomain"])
	payload, _ := json.Marshal(map[string]string{"grant_type": "authorization_code", "code": r.URL.Query().Get("code"), "client_id": os.Getenv("ZENDESK_OAUTH_CLIENT_ID"), "client_secret": os.Getenv("ZENDESK_OAUTH_CLIENT_SECRET"), "redirect_uri": s.Config.AppURL + "/api/connectors/zendesk/callback", "scope": "read"})
	request, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, "https://"+subdomain+".zendesk.com/oauth/tokens", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	var token map[string]interface{}
	if err = s.doJSON(request, &token); err != nil {
		return err
	}
	access, refresh := stringValue(token["access_token"]), stringValue(token["refresh_token"])
	if refresh == "" {
		refresh = access
	}
	id, err := s.upsertOAuth(r, state, "zendesk", subdomain, []string{"read"}, access, refresh, time.Now().Add(time.Duration(numberInt(token["expires_in"]))*time.Second), map[string]interface{}{"subdomain": subdomain})
	if err != nil {
		return err
	}
	http.Redirect(w, r, s.Config.AppURL+"/connectors?connected=zendesk", http.StatusFound)
	_ = id
	return nil
}
func (s *Server) zendeskResources(w http.ResponseWriter, _ *http.Request) error {
	jsonResponse(w, http.StatusOK, map[string]interface{}{"resources": []string{"tickets", "users", "organizations"}})
	return nil
}

func (s *Server) upsertOAuth(r *http.Request, state oauthState, provider, email string, scopes []string, access, refresh string, expires time.Time, extra map[string]interface{}) (string, error) {
	encryptedAccess, err := s.encryptToken(access)
	if err != nil {
		return "", err
	}
	encryptedRefresh, err := s.encryptToken(refresh)
	if err != nil {
		return "", err
	}
	var id string
	err = s.DB.TenantTx(r.Context(), state.TenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `INSERT INTO connector_instances (tenant_id,user_id,provider,provider_account_email,scopes,access_token,refresh_token,expires_at,extra) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(tenant_id,user_id,provider,provider_account_email) DO UPDATE SET scopes=EXCLUDED.scopes,access_token=EXCLUDED.access_token,refresh_token=EXCLUDED.refresh_token,expires_at=EXCLUDED.expires_at,extra=EXCLUDED.extra RETURNING id`, state.TenantID, state.UserID, provider, email, scopes, encryptedAccess, encryptedRefresh, expires, extra).Scan(&id)
	})
	return id, err
}
func (s *Server) liveToken(r *http.Request, row map[string]interface{}) (string, error) {
	access, err := s.decryptToken(stringValue(row["access_token"]))
	if err != nil {
		return "", err
	}
	expires := timeValue(row["expires_at"])
	if expires.After(time.Now().Add(time.Minute)) {
		return access, nil
	}
	refresh, err := s.decryptToken(stringValue(row["refresh_token"]))
	if err != nil {
		return "", err
	}
	provider := stringValue(row["provider"])
	values := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}
	endpoint := ""
	if provider == "google" {
		endpoint = "https://oauth2.googleapis.com/token"
		values.Set("client_id", os.Getenv("GOOGLE_CLIENT_ID"))
		values.Set("client_secret", os.Getenv("GOOGLE_CLIENT_SECRET"))
	} else if provider == "microsoft" {
		endpoint = "https://login.microsoftonline.com/" + env("AZURE_TENANT_ID", "common") + "/oauth2/v2.0/token"
		values.Set("client_id", os.Getenv("AZURE_CLIENT_ID"))
		values.Set("client_secret", os.Getenv("AZURE_CLIENT_SECRET"))
		values.Set("scope", strings.Join(microsoftScopes, " "))
	} else {
		return access, nil
	}
	var token map[string]interface{}
	if err = s.postFormJSON(r, endpoint, values, &token); err != nil {
		return "", err
	}
	access = stringValue(token["access_token"])
	encrypted, err := s.encryptToken(access)
	if err != nil {
		return "", err
	}
	tenant := tenantFrom(r)
	err = s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(r.Context(), `UPDATE connector_instances SET access_token=$1,expires_at=$2 WHERE id=$3`, encrypted, time.Now().Add(time.Duration(numberInt(token["expires_in"]))*time.Second), row["id"])
		return err
	})
	return access, err
}
func (s *Server) connectorRefresh(w http.ResponseWriter, r *http.Request) error {
	row, err := s.connection(r, r.PathValue("connectionId"))
	if err != nil {
		return err
	}
	if row == nil {
		return notFound(ErrNotFound, "not found")
	}
	token, err := s.liveToken(r, row)
	if err != nil {
		return err
	}
	_ = token
	jsonResponse(w, http.StatusOK, map[string]interface{}{"ok": true, "expiresAt": row["expires_at"]})
	return nil
}
func (s *Server) pickOAuth(r *http.Request, provider string) (map[string]interface{}, error) {
	tenant := tenantFrom(r)
	id := r.URL.Query().Get("connectionId")
	query := `SELECT * FROM connector_instances WHERE provider=$1 AND kind='oauth'`
	args := []interface{}{provider}
	if id != "" {
		query += ` AND id=$2`
		args = append(args, id)
	}
	query += ` ORDER BY created_at DESC LIMIT 1`
	rows, err := tenantQueryRows(r.Context(), s.DB, tenant.TenantID, query, args...)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}
func (s *Server) providerGet(r *http.Request, provider, path string, target interface{}) error {
	row, err := s.pickOAuth(r, provider)
	if err != nil || row == nil {
		return notFound(ErrNotFound, "no "+provider+" connection")
	}
	token, err := s.liveToken(r, row)
	if err != nil {
		return err
	}
	request, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	return s.doJSON(request, target)
}
func (s *Server) googleSpreadsheets(w http.ResponseWriter, r *http.Request) error {
	var data map[string]interface{}
	err := s.providerGet(r, "google", "https://www.googleapis.com/drive/v3/files?q="+url.QueryEscape("mimeType='application/vnd.google-apps.spreadsheet' and trashed=false")+"&pageSize=50&fields=files(id,name,modifiedTime,webViewLink)", &data)
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"files": data["files"], "connectionId": r.URL.Query().Get("connectionId")})
	return nil
}
func (s *Server) googleSheets(w http.ResponseWriter, r *http.Request) error {
	var data map[string]interface{}
	if err := s.providerGet(r, "google", "https://sheets.googleapis.com/v4/spreadsheets/"+url.PathEscape(r.PathValue("id"))+"?fields=properties,sheets.properties", &data); err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"title": data["properties"], "sheets": data["sheets"]})
	return nil
}
func (s *Server) googleSheetPreview(w http.ResponseWriter, r *http.Request) error {
	var data map[string]interface{}
	path := "https://sheets.googleapis.com/v4/spreadsheets/" + url.PathEscape(r.PathValue("id")) + "/values/" + url.PathEscape(r.PathValue("name")+"!A1:Z6")
	if err := s.providerGet(r, "google", path, &data); err != nil {
		return err
	}
	values, _ := data["values"].([]interface{})
	var headers interface{} = []interface{}{}
	rows := []interface{}{}
	if len(values) > 0 {
		headers = values[0]
		rows = values[1:]
		if len(rows) > 5 {
			rows = rows[:5]
		}
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"headers": headers, "rows": rows})
	return nil
}
func (s *Server) googleFolders(w http.ResponseWriter, r *http.Request) error {
	parent := r.URL.Query().Get("parent")
	if parent == "" {
		parent = "root"
	}
	var data map[string]interface{}
	path := "https://www.googleapis.com/drive/v3/files?q=" + url.QueryEscape("'"+parent+"' in parents and mimeType='application/vnd.google-apps.folder' and trashed=false") + "&pageSize=100&fields=files(id,name)"
	if err := s.providerGet(r, "google", path, &data); err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"parent": parent, "folders": data["files"]})
	return nil
}
func (s *Server) googleFilePreview(w http.ResponseWriter, r *http.Request) error {
	var data map[string]interface{}
	if err := s.providerGet(r, "google", "https://www.googleapis.com/drive/v3/files/"+url.PathEscape(r.PathValue("id"))+"?fields=id,name,mimeType,modifiedTime,size,webViewLink", &data); err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"file": data})
	return nil
}
func (s *Server) graphGet(r *http.Request, path string, target interface{}) error {
	return s.providerGet(r, "microsoft", "https://graph.microsoft.com/v1.0"+path, target)
}
func (s *Server) microsoftDrives(w http.ResponseWriter, r *http.Request) error {
	var drive map[string]interface{}
	drives := []interface{}{}
	if s.graphGet(r, "/me/drive", &drive) == nil {
		drives = append(drives, map[string]interface{}{"id": drive["id"], "name": "OneDrive — Personal", "driveType": drive["driveType"]})
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"drives": drives, "connectionId": r.URL.Query().Get("connectionId")})
	return nil
}
func (s *Server) microsoftItems(w http.ResponseWriter, r *http.Request) error {
	parent := r.URL.Query().Get("parent")
	if parent == "" {
		parent = "root"
	}
	var data map[string]interface{}
	if err := s.graphGet(r, "/drives/"+url.PathEscape(r.PathValue("driveId"))+"/items/"+url.PathEscape(parent)+"/children", &data); err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"items": data["value"]})
	return nil
}
func (s *Server) microsoftSheets(w http.ResponseWriter, r *http.Request) error {
	drive := r.URL.Query().Get("driveId")
	if drive == "" {
		return badRequest(ErrInvalidRequest, "driveId required")
	}
	var data map[string]interface{}
	if err := s.graphGet(r, "/drives/"+url.PathEscape(drive)+"/items/"+url.PathEscape(r.PathValue("itemId"))+"/workbook/worksheets", &data); err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"sheets": data["value"]})
	return nil
}
func (s *Server) microsoftPreview(w http.ResponseWriter, r *http.Request) error {
	drive := r.URL.Query().Get("driveId")
	if drive == "" {
		return badRequest(ErrInvalidRequest, "driveId required")
	}
	var data map[string]interface{}
	path := "/drives/" + url.PathEscape(drive) + "/items/" + url.PathEscape(r.PathValue("itemId")) + "/workbook/worksheets('" + url.PathEscape(r.PathValue("name")) + "')/usedRange(valuesOnly=true)"
	if err := s.graphGet(r, path, &data); err != nil {
		return err
	}
	values, _ := data["values"].([]interface{})
	var headers interface{} = []interface{}{}
	rows := []interface{}{}
	if len(values) > 0 {
		headers = values[0]
		rows = values[1:]
		if len(rows) > 5 {
			rows = rows[:5]
		}
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"headers": headers, "rows": rows})
	return nil
}
