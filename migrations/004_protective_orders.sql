-- Phase D: add protective order tracking to positions

ALTER TABLE positions ADD COLUMN IF NOT EXISTS sl_order_id BIGINT;
ALTER TABLE positions ADD COLUMN IF NOT EXISTS tp_order_id BIGINT;

-- TODO: link orders.position_id to positions table.
-- Broker does not populate orders.position_id yet; this is deferred to
-- restart recovery / reconciliation work when PaperBroker rehydrates from DB.
