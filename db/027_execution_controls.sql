-- Durable intent is acknowledged independently from delivery to Temporal.
ALTER TABLE executions
  ADD COLUMN control_state TEXT NOT NULL DEFAULT 'active'
    CHECK (control_state IN ('active', 'paused', 'cancel_requested')),
  ADD COLUMN control_revision BIGINT NOT NULL DEFAULT 0 CHECK (control_revision >= 0),
  ADD COLUMN control_next_delivery_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  ADD COLUMN control_delivered_revision BIGINT NOT NULL DEFAULT 0,
  ADD CONSTRAINT executions_control_delivery_bounds CHECK
    (control_delivered_revision >= 0 AND control_delivered_revision <= control_revision);

UPDATE executions SET control_state='paused' WHERE phase='paused';

CREATE INDEX executions_pending_control ON executions (environment, control_next_delivery_at, tenant_id, id)
  WHERE control_delivered_revision < control_revision
    AND phase NOT IN ('completed', 'failed', 'cancelled');
-- Existing execution RLS remains in force. No independent outbox can outlive its execution.
