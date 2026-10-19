package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/dataflow-poc/workflow-go/internal/model"
	"github.com/jackc/pgx/v5"
)

func (s *Server) registerBillingWebhook(mux *http.ServeMux) {
	mux.Handle("POST /api/billing/webhook", s.rateLimit("billing-webhook", 60, time.Minute, handle(s.billingWebhook)))
}
func (s *Server) registerBilling(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/billing/usage", handle(s.billingUsage))
	mux.Handle("POST /api/billing/orders", owner(handle(s.billingOrder)))
	mux.HandleFunc("GET /api/billing/history", handle(s.billingHistory))
}

func daysUntilReset() int {
	now := time.Now().UTC()
	next := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	days := int(next.Sub(now).Hours() / 24)
	if next.Sub(now) > time.Duration(days)*24*time.Hour {
		days++
	}
	return days
}
func (s *Server) billingUsage(w http.ResponseWriter, r *http.Request) error {
	tenant := tenantFrom(r)
	out := map[string]interface{}{}
	err := s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		var free, extra int
		if err := tx.QueryRow(r.Context(), `INSERT INTO billing_plans (tenant_id) VALUES ($1) ON CONFLICT(tenant_id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id RETURNING free_tier_limit,extra_quota`, tenant.TenantID).Scan(&free, &extra); err != nil {
			return err
		}
		used := 0
		_ = tx.QueryRow(r.Context(), `SELECT execution_count FROM usage_counters WHERE tenant_id=$1 AND month=$2`, tenant.TenantID, monthStart()).Scan(&used)
		out = map[string]interface{}{"used": used, "limit": free + extra, "free_tier": free, "extra_quota": extra, "daysUntilReset": daysUntilReset(), "metered": s.Config.BillingEnabled}
		return nil
	})
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, out)
	return nil
}
func (s *Server) billingOrder(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Units int `json:"units"`
	}
	if !decodeJSON(w, r, &body) {
		return nil
	}
	if body.Units < 1 || body.Units > 100 {
		return badRequest(ErrInvalidRequest, "units must be an integer between 1 and 100")
	}
	key, secret := os.Getenv("RAZORPAY_KEY_ID"), os.Getenv("RAZORPAY_KEY_SECRET")
	if key == "" || secret == "" {
		return &HTTPError{Status: http.StatusInternalServerError, Message: "razorpay not configured"}
	}
	tenant := tenantFrom(r)
	price := 0
	err := s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `INSERT INTO billing_plans (tenant_id) VALUES ($1) ON CONFLICT(tenant_id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id RETURNING price_per_5_paise`, tenant.TenantID).Scan(&price)
	})
	if err != nil {
		return err
	}
	amount := price * body.Units
	payload, _ := json.Marshal(map[string]interface{}{"amount": amount, "currency": "INR", "notes": map[string]string{"tenantId": tenant.TenantID, "units": fmt.Sprint(body.Units)}})
	request, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, "https://api.razorpay.com/v1/orders", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.SetBasicAuth(key, secret)
	var order struct {
		ID string `json:"id"`
	}
	if err := s.doJSON(request, &order); err != nil {
		return &HTTPError{Status: http.StatusBadGateway, Message: "razorpay error"}
	}
	err = s.DB.TenantTx(r.Context(), tenant.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(r.Context(), `INSERT INTO payment_orders (tenant_id,razorpay_order_id,amount_paise,quota_units,status) VALUES ($1,$2,$3,$4,'created')`, tenant.TenantID, order.ID, amount, body.Units)
		return err
	})
	if err != nil {
		return err
	}
	s.audit(r.Context(), tenant, "payment.order_created", order.ID, map[string]int{"units": body.Units, "amountPaise": amount}, r)
	jsonResponse(w, http.StatusOK, map[string]interface{}{"orderId": order.ID, "amount": amount, "currency": "INR", "razorpayKey": key})
	return nil
}
func (s *Server) billingHistory(w http.ResponseWriter, r *http.Request) error {
	tenant := tenantFrom(r)
	rows, err := tenantQueryRows(r.Context(), s.DB, tenant.TenantID, `SELECT id,razorpay_order_id,razorpay_payment_id,amount_paise,quota_units,status,created_at,paid_at FROM payment_orders ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, rows)
	return nil
}

func (s *Server) billingWebhook(w http.ResponseWriter, r *http.Request) error {
	secret := os.Getenv("RAZORPAY_WEBHOOK_SECRET")
	if secret == "" {
		return &HTTPError{Status: http.StatusInternalServerError, Message: "webhook not configured"}
	}
	signature := r.Header.Get("X-Razorpay-Signature")
	if signature == "" {
		return &HTTPError{Status: http.StatusUnauthorized, Message: "missing signature"}
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		return badRequest(ErrInvalidRequest, "bad json")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	provided, err := hex.DecodeString(signature)
	if err != nil || !hmac.Equal(provided, mac.Sum(nil)) {
		return &HTTPError{Status: http.StatusUnauthorized, Message: "bad signature"}
	}
	var event struct {
		Event   string `json:"event"`
		Payload map[string]struct {
			Entity map[string]interface{} `json:"entity"`
		} `json:"payload"`
	}
	if json.Unmarshal(body, &event) != nil {
		return badRequest(ErrInvalidRequest, "bad json")
	}
	payment := event.Payload["payment"].Entity
	order := event.Payload["order"].Entity
	orderID := stringValue(payment["order_id"])
	if orderID == "" {
		orderID = stringValue(order["id"])
	}
	switch event.Event {
	case "payment.captured", "order.paid":
		if orderID == "" {
			return badRequest(ErrInvalidRequest, "no order_id")
		}
		// Razorpay calls before any tenant is known; resolve the order's tenant
		// through the SECURITY DEFINER resolver, then work inside that tenant.
		var orderRowID, tenantID string
		if err := s.DB.Pool.QueryRow(r.Context(), `SELECT id,tenant_id FROM resolve_payment_order($1)`, orderID).Scan(&orderRowID, &tenantID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return notFound(ErrNotFound, "unknown order")
			}
			return err
		}
		var units int
		alreadyPaid := false
		err = s.DB.TenantTx(r.Context(), tenantID, func(tx pgx.Tx) error {
			var status string
			if err := tx.QueryRow(r.Context(), `SELECT status,quota_units FROM payment_orders WHERE id=$1 FOR UPDATE`, orderRowID).Scan(&status, &units); err != nil {
				return err
			}
			if status == "paid" {
				alreadyPaid = true
				return nil
			}
			if _, err := tx.Exec(r.Context(), `UPDATE payment_orders SET status='paid',paid_at=now(),razorpay_payment_id=$2 WHERE id=$1`, orderRowID, nullString(stringValue(payment["id"]))); err != nil {
				return err
			}
			_, err := tx.Exec(r.Context(), `INSERT INTO billing_plans (tenant_id,extra_quota) VALUES ($1,$2) ON CONFLICT(tenant_id) DO UPDATE SET extra_quota=billing_plans.extra_quota+$2,updated_at=now()`, tenantID, units*5)
			return err
		})
		if err != nil {
			return err
		}
		if alreadyPaid {
			jsonResponse(w, http.StatusOK, map[string]bool{"ok": true, "idempotent": true})
			return nil
		}
		s.audit(r.Context(), model.TenantContext{TenantID: tenantID}, "payment.captured", orderID, map[string]interface{}{"paymentId": payment["id"], "units": units}, r)
	case "payment.failed":
		var tenantID string
		if orderID != "" && s.DB.Pool.QueryRow(r.Context(), `SELECT tenant_id FROM resolve_payment_order($1)`, orderID).Scan(&tenantID) == nil {
			_ = s.DB.TenantTx(r.Context(), tenantID, func(tx pgx.Tx) error {
				_, err := tx.Exec(r.Context(), `UPDATE payment_orders SET status='failed' WHERE razorpay_order_id=$1 AND status='created'`, orderID)
				return err
			})
		}
	}
	jsonResponse(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}
