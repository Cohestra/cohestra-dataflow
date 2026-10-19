-- Public entry points (webhook triggers, payment webhooks, OAuth sign-up with
-- a pending invitation) run before any tenant is known. Under row-level
-- security the API role (dataflow_app) sees no rows there, so each lookup goes
-- through a narrow SECURITY DEFINER resolver, like resolve_openlineage_tenant.

CREATE OR REPLACE FUNCTION resolve_webhook_pipelines(p_path TEXT)
RETURNS TABLE (id UUID, tenant_id UUID, environment TEXT, definition JSONB)
LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=public AS $$
  SELECT p.id, p.tenant_id, p.environment, p.definition
    FROM pipelines p
   WHERE p.status='active'
     AND p.definition->'trigger'->>'type'='webhook'
     AND p.definition->'trigger'->>'path'=p_path
   ORDER BY (p.environment='prod') DESC, p.created_at DESC
$$;
REVOKE ALL ON FUNCTION resolve_webhook_pipelines(TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION resolve_webhook_pipelines(TEXT) TO dataflow_app;

-- Webhook paths are global, so activation refuses a path another tenant is
-- already serving. Returns true when a different tenant owns the path.
CREATE OR REPLACE FUNCTION webhook_path_taken(p_path TEXT, p_tenant UUID)
RETURNS BOOLEAN
LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=public AS $$
  SELECT EXISTS (
    SELECT 1 FROM pipelines p
     WHERE p.status='active'
       AND p.tenant_id<>p_tenant
       AND p.definition->'trigger'->>'type'='webhook'
       AND p.definition->'trigger'->>'path'=p_path)
$$;
REVOKE ALL ON FUNCTION webhook_path_taken(TEXT, UUID) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION webhook_path_taken(TEXT, UUID) TO dataflow_app;

CREATE OR REPLACE FUNCTION resolve_payment_order(p_razorpay_order_id TEXT)
RETURNS TABLE (id UUID, tenant_id UUID)
LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=public AS $$
  SELECT o.id, o.tenant_id FROM payment_orders o WHERE o.razorpay_order_id=p_razorpay_order_id
$$;
REVOKE ALL ON FUNCTION resolve_payment_order(TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION resolve_payment_order(TEXT) TO dataflow_app;

-- Accepts the newest pending invitation for an email whose ownership the
-- identity provider has verified, and returns where it leads.
CREATE OR REPLACE FUNCTION claim_email_invitation(p_email TEXT)
RETURNS TABLE (tenant_id UUID, role TEXT)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=public AS $$
BEGIN
  RETURN QUERY
  UPDATE user_invitations i SET accepted_at=now()
   WHERE i.token_hash = (
     SELECT c.token_hash FROM user_invitations c
      WHERE c.email=p_email AND c.accepted_at IS NULL AND c.expires_at>now()
      ORDER BY c.expires_at DESC LIMIT 1 FOR UPDATE)
  RETURNING i.tenant_id, i.role;
END $$;
REVOKE ALL ON FUNCTION claim_email_invitation(TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION claim_email_invitation(TEXT) TO dataflow_app;
