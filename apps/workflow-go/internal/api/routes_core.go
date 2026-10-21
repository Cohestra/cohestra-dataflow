package api

import (
	"crypto/tls"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dataflow-poc/workflow-go/internal/enterprise"
	"github.com/jackc/pgx/v5"
	smtp "github.com/wneessen/go-mail/smtp"
)

var paidFeatureKeys = map[string]bool{
	"advancedConnectors": true, "realtime": true, "sparkSql": true, "flinkSql": true, "statefulProcessing": true,
	"deepObservability": true, "governance": true,
}

func (s *Server) paidFeatures(r *http.Request) (map[string]bool, error) {
	features := map[string]bool{"advancedConnectors": false, "realtime": false, "sparkSql": false, "flinkSql": false, "statefulProcessing": false, "deepObservability": false, "governance": s.Config.Edition == "enterprise"}
	if !enterprise.Build {
		// Community builds carry no enterprise code; entitlements cannot turn on
		// features that are not in the binary.
		return map[string]bool{}, nil
	}
	tenant := tenantFrom(r)
	rows, err := tenantQueryRows(r.Context(), s.DB, tenant.TenantID, `SELECT feature,enabled FROM tenant_feature_entitlements WHERE tenant_id=$1`, tenant.TenantID)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		feature := stringValue(row["feature"])
		if paidFeatureKeys[feature] {
			features[feature] = boolValue(row["enabled"])
		}
	}
	return features, nil
}

func (s *Server) requireFeature(feature string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		features, err := s.paidFeatures(r)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, ErrInternal, "internal error", nil)
			return
		}
		if !features[feature] {
			jsonResponse(w, http.StatusPaymentRequired, map[string]interface{}{"error": feature + " is not enabled for this workspace", "feature": feature})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) registerEdition(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/edition", handle(s.editionGet))
	mux.Handle("PUT /api/edition/features/{feature}", owner(handle(s.editionPut)))
	mux.Handle("GET /api/edition/audit-export", owner(s.requireFeature("governance", handle(s.auditExport))))
}

func (s *Server) editionGet(w http.ResponseWriter, r *http.Request) error {
	features, err := s.paidFeatures(r)
	if err != nil {
		return err
	}
	availability := map[string]bool{}
	for key := range paidFeatureKeys {
		availability[key] = true
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"edition": s.Config.Edition, "features": features, "availability": availability})
	return nil
}

func (s *Server) editionPut(w http.ResponseWriter, r *http.Request) error {
	if !s.Config.InternalDemoFeatures {
		return &HTTPError{Status: http.StatusForbidden, Code: ErrForbidden, Message: "feature entitlements are managed by your plan; self-service changes require INTERNAL_DEMO_FEATURES"}
	}
	feature := r.PathValue("feature")
	var body struct {
		Enabled interface{} `json:"enabled"`
	}
	if !decodeJSON(w, r, &body) {
		return nil
	}
	enabled, ok := body.Enabled.(bool)
	if !paidFeatureKeys[feature] || !ok {
		return badRequest(ErrInvalidRequest, "valid feature and boolean enabled are required")
	}
	tenant := tenantFrom(r)
	err := s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(r.Context(), `INSERT INTO tenant_feature_entitlements (tenant_id,feature,enabled)
      VALUES ($1,$2,$3) ON CONFLICT (tenant_id,feature) DO UPDATE SET enabled=EXCLUDED.enabled,updated_at=now()`, tenant.TenantID, feature, enabled)
		return err
	})
	if err != nil {
		return err
	}
	s.audit(r.Context(), tenant, "feature.updated", feature, map[string]bool{"enabled": enabled}, r)
	features, err := s.paidFeatures(r)
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"features": features})
	return nil
}

func (s *Server) auditExport(w http.ResponseWriter, r *http.Request) error {
	tenant := tenantFrom(r)
	rows, err := tenantQueryRows(r.Context(), s.DB, tenant.TenantID, `SELECT created_at,action,resource,user_id,ip_address,metadata FROM audit_log ORDER BY created_at DESC LIMIT 10000`)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="audit-log.csv"`)
	writer := csv.NewWriter(w)
	header := []string{"created_at", "action", "resource", "user_id", "ip_address", "metadata"}
	_ = writer.Write(header)
	for _, row := range rows {
		values := make([]string, len(header))
		for i, key := range header {
			if key == "metadata" {
				b, _ := json.Marshal(row[key])
				values[i] = string(b)
			} else {
				values[i] = stringValue(row[key])
			}
		}
		_ = writer.Write(values)
	}
	writer.Flush()
	return writer.Error()
}

func (s *Server) registerAlerts(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/alerts", handle(s.alertsList))
	mux.HandleFunc("POST /api/alerts/{id}/acknowledge", handle(s.alertAcknowledge))
	mux.HandleFunc("POST /api/alerts/{id}/resolve", handle(s.alertResolve))
	mux.HandleFunc("POST /api/alerts/{id}/retry-notification", handle(s.alertRetryNotification))
}

func (s *Server) alertsList(w http.ResponseWriter, r *http.Request) error {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "active"
	}
	valid := map[string]bool{"active": true, "open": true, "acknowledged": true, "resolved": true, "all": true}
	if !valid[status] {
		return badRequest(ErrInvalidRequest, "invalid alert status")
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	tenant := tenantFrom(r)
	rows, err := tenantQueryRows(r.Context(), s.DB, tenant.TenantID, `SELECT a.*,p.name AS pipeline_name,p.environment,
      u.email AS acknowledged_by_email,delivery.sent_at AS notification_sent_at,
      delivery.attempts AS notification_attempts,delivery.last_error AS notification_error
      FROM pipeline_alerts a JOIN pipelines p ON p.id=a.pipeline_id LEFT JOIN users u ON u.id=a.acknowledged_by
      LEFT JOIN LATERAL (SELECT sent_at,attempts,last_error FROM pipeline_alert_notification_outbox WHERE alert_id=a.id ORDER BY created_at DESC LIMIT 1) delivery ON true
      WHERE ($1='all' OR ($1='active' AND a.status IN ('open','acknowledged')) OR a.status=$1)
      ORDER BY CASE a.severity WHEN 'critical' THEN 0 ELSE 1 END,a.last_seen_at DESC LIMIT $2`, status, limit)
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, rows)
	return nil
}

func (s *Server) updateAlert(w http.ResponseWriter, r *http.Request, action string) error {
	tenant := tenantFrom(r)
	var query string
	if action == "acknowledge" {
		query = `UPDATE pipeline_alerts SET status='acknowledged',acknowledged_at=now(),acknowledged_by=$2 WHERE id=$1 AND status='open' RETURNING *`
	} else {
		query = `UPDATE pipeline_alerts SET status='resolved',resolved_at=now() WHERE id=$1 AND status IN ('open','acknowledged') RETURNING *`
	}
	var row map[string]interface{}
	err := s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		args := []interface{}{r.PathValue("id")}
		if action == "acknowledge" {
			args = append(args, tenant.UserID)
		}
		rows, err := tx.Query(r.Context(), query, args...)
		if err != nil {
			return err
		}
		row, err = oneMap(rows)
		return err
	})
	if err != nil {
		return err
	}
	if row == nil {
		if action == "acknowledge" {
			return notFound(ErrNotFound, "open alert not found")
		}
		return notFound(ErrNotFound, "active alert not found")
	}
	s.audit(r.Context(), tenant, "pipeline_alert."+map[string]string{"acknowledge": "acknowledged", "resolve": "resolved"}[action], stringValue(row["id"]), map[string]interface{}{"pipelineId": row["pipeline_id"], "kind": row["kind"]}, r)
	jsonResponse(w, http.StatusOK, row)
	return nil
}

func (s *Server) alertAcknowledge(w http.ResponseWriter, r *http.Request) error {
	return s.updateAlert(w, r, "acknowledge")
}
func (s *Server) alertResolve(w http.ResponseWriter, r *http.Request) error {
	return s.updateAlert(w, r, "resolve")
}

func (s *Server) alertRetryNotification(w http.ResponseWriter, r *http.Request) error {
	tenant := tenantFrom(r)
	changed := false
	err := s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		command, err := tx.Exec(r.Context(), `UPDATE pipeline_alert_notification_outbox n SET attempts=0,next_attempt_at=now(),last_error=NULL
      FROM pipeline_alerts a WHERE n.alert_id=a.id AND a.id=$1 AND n.sent_at IS NULL`, r.PathValue("id"))
		changed = command.RowsAffected() > 0
		return err
	})
	if err != nil {
		return err
	}
	if !changed {
		return notFound(ErrNotFound, "failed or pending notification not found")
	}
	s.audit(r.Context(), tenant, "pipeline_alert.notification_retried", r.PathValue("id"), nil, r)
	jsonResponse(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}

func (s *Server) registerTeam(mux *http.ServeMux) {
	mux.Handle("POST /api/team/invitations", owner(handle(s.inviteCreate)))
	mux.Handle("GET /api/team/invitations", owner(handle(s.inviteList)))
	mux.Handle("DELETE /api/team/invitations/{email}", owner(handle(s.inviteDelete)))
	mux.HandleFunc("GET /api/team/members", handle(s.membersList))
	mux.Handle("POST /api/team/tokens", owner(handle(s.tokenCreate)))
	mux.Handle("GET /api/team/tokens", owner(handle(s.tokenList)))
	mux.Handle("DELETE /api/team/tokens/{id}", owner(handle(s.tokenDelete)))
}

func (s *Server) inviteCreate(w http.ResponseWriter, r *http.Request) error {
	var body struct{ Email, Role string }
	if !decodeJSON(w, r, &body) {
		return nil
	}
	if body.Email == "" {
		return badRequest(ErrInvalidRequest, "email required")
	}
	var existing int
	if err := s.DB.Pool.QueryRow(r.Context(), `SELECT 1 FROM users WHERE email=$1`, body.Email).Scan(&existing); err == nil {
		return &HTTPError{Status: http.StatusConflict, Message: "user already exists"}
	}
	role := "member"
	if body.Role == "owner" {
		role = "owner"
	}
	tenant := tenantFrom(r)
	token := tenant.TenantID + "." + randomToken()
	if err := s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(r.Context(), `INSERT INTO user_invitations (token_hash,tenant_id,invited_by,email,role,expires_at)
      VALUES ($1,$2,$3,$4,$5,now()+interval '24 hours')`, sha256Hex(token), tenant.TenantID, tenant.UserID, body.Email, role)
		return err
	}); err != nil {
		return err
	}
	if err := s.sendInvite(body.Email, token, tenant.Email); err != nil {
		_ = s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
			_, deleteErr := tx.Exec(r.Context(), `DELETE FROM user_invitations WHERE token_hash=$1 AND tenant_id=$2 AND accepted_at IS NULL`, sha256Hex(token), tenant.TenantID)
			return deleteErr
		})
		return &HTTPError{Status: http.StatusBadGateway, Message: "invite email failed to send"}
	}
	s.audit(r.Context(), tenant, "team.invited", body.Email, map[string]string{"role": role}, r)
	jsonResponse(w, http.StatusCreated, map[string]interface{}{"ok": true, "expiresInHours": 24})
	return nil
}

func (s *Server) sendInvite(email, token, inviter string) error {
	link := s.Config.AppURL + "/accept-invite?token=" + urlQueryEscape(token)
	return s.sendMail(email, "You're invited to DataFlow", fmt.Sprintf("<p>%s invited you to DataFlow.</p><p><a href=\"%s\">Accept invitation</a></p>", html.EscapeString(inviter), link))
}

// sendMail delivers one HTML email through the configured SMTP server.
func (s *Server) sendMail(email, subject, htmlBody string) error {
	body := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n%s", s.Config.SMTPFrom, email, subject, htmlBody)

	addr := s.Config.SMTPHost + ":" + s.Config.SMTPPort

	var auth smtp.Auth
	if s.Config.SMTPUser != "" {
		auth = smtp.PlainAuth("", s.Config.SMTPUser, s.Config.SMTPPass, s.Config.SMTPHost, false)
	}

	// Port 465 uses implicit TLS (SMTPS); smtp.SendMail only does STARTTLS so
	// we must dial TLS first and drive the smtp.Client manually.
	if s.Config.SMTPPort == "465" {
		tlsCfg := &tls.Config{ServerName: s.Config.SMTPHost}
		conn, err := tls.Dial("tcp", addr, tlsCfg)
		if err != nil {
			return fmt.Errorf("smtp tls dial: %w", err)
		}
		defer conn.Close()
		client, err := smtp.NewClient(conn, s.Config.SMTPHost)
		if err != nil {
			return fmt.Errorf("smtp new client: %w", err)
		}
		defer client.Close()
		if auth != nil {
			if err = client.Auth(auth); err != nil {
				return fmt.Errorf("smtp auth: %w", err)
			}
		}
		if err = client.Mail(s.Config.SMTPFrom); err != nil {
			return fmt.Errorf("smtp MAIL FROM: %w", err)
		}
		if err = client.Rcpt(email); err != nil {
			return fmt.Errorf("smtp RCPT TO: %w", err)
		}
		w, err := client.Data()
		if err != nil {
			return fmt.Errorf("smtp DATA: %w", err)
		}
		if _, err = fmt.Fprint(w, body); err != nil {
			return fmt.Errorf("smtp write body: %w", err)
		}
		if err = w.Close(); err != nil {
			return fmt.Errorf("smtp close data writer: %w", err)
		}
		return client.Quit()
	}

	// Ports 587 / 25 / 1025 — use STARTTLS via smtp.SendMail.
	return smtp.SendMail(addr, auth, s.Config.SMTPFrom, []string{email}, []byte(body))
}

func (s *Server) inviteList(w http.ResponseWriter, r *http.Request) error {
	tenant := tenantFrom(r)
	rows, err := tenantQueryRows(r.Context(), s.DB, tenant.TenantID, `SELECT email,role,expires_at,accepted_at,created_at FROM user_invitations WHERE accepted_at IS NULL AND expires_at>now() ORDER BY created_at DESC`)
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, rows)
	return nil
}

func (s *Server) inviteDelete(w http.ResponseWriter, r *http.Request) error {
	tenant := tenantFrom(r)
	err := s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(r.Context(), `DELETE FROM user_invitations WHERE email=$1 AND accepted_at IS NULL`, r.PathValue("email"))
		return err
	})
	if err != nil {
		return err
	}
	s.audit(r.Context(), tenant, "team.invite_revoked", r.PathValue("email"), nil, r)
	jsonResponse(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}

func (s *Server) membersList(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.DB.Pool.Query(r.Context(), `SELECT id,email,role,email_verified,created_at FROM users WHERE tenant_id=$1 ORDER BY created_at`, tenantFrom(r).TenantID)
	if err != nil {
		return err
	}
	result, err := rowsToMaps(rows)
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, result)
	return nil
}

func (s *Server) tokenCreate(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Name, Role    string
		ExpiresInDays float64 `json:"expiresInDays"`
	}
	if !decodeJSON(w, r, &body) {
		return nil
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		return badRequest(ErrInvalidRequest, "name required")
	}
	role := "member"
	if body.Role == "owner" {
		role = "owner"
	}
	var expires interface{}
	if body.ExpiresInDays > 0 {
		expires = time.Now().Add(time.Duration(body.ExpiresInDays*24) * time.Hour)
	}
	token, tenant := randomToken(), tenantFrom(r)
	var row map[string]interface{}
	err := s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `INSERT INTO api_tokens (tenant_id,name,token_hash,role,created_by,expires_at) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id,name,role,created_at,expires_at`, tenant.TenantID, body.Name, sha256Hex(token), role, tenant.UserID, expires)
		if err != nil {
			return err
		}
		row, err = oneMap(rows)
		return err
	})
	if err != nil {
		return err
	}
	row["token"] = token
	s.audit(r.Context(), tenant, "team.token_created", stringValue(row["id"]), map[string]string{"name": body.Name, "role": role}, r)
	jsonResponse(w, http.StatusCreated, row)
	return nil
}

func (s *Server) tokenList(w http.ResponseWriter, r *http.Request) error {
	tenant := tenantFrom(r)
	rows, err := tenantQueryRows(r.Context(), s.DB, tenant.TenantID, `SELECT t.id,t.name,t.role,t.created_at,t.expires_at,t.revoked_at,u.email AS created_by_email FROM api_tokens t JOIN users u ON u.id=t.created_by WHERE t.tenant_id=$1 ORDER BY t.created_at DESC`, tenant.TenantID)
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, rows)
	return nil
}

func (s *Server) tokenDelete(w http.ResponseWriter, r *http.Request) error {
	tenant := tenantFrom(r)
	changed := false
	err := s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		command, err := tx.Exec(r.Context(), `UPDATE api_tokens SET revoked_at=now() WHERE id=$1 AND tenant_id=$2 AND revoked_at IS NULL`, r.PathValue("id"), tenant.TenantID)
		changed = command.RowsAffected() > 0
		return err
	})
	if err != nil {
		return err
	}
	if !changed {
		return notFound(ErrNotFound, "token not found or already revoked")
	}
	s.audit(r.Context(), tenant, "team.token_revoked", r.PathValue("id"), nil, r)
	jsonResponse(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}

func urlQueryEscape(value string) string { return strings.ReplaceAll(value, "+", "%2B") }
