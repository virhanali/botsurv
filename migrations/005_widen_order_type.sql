-- Phase E: widen orders.order_type to support TAKE_PROFIT_MARKET (18 chars exceeds VARCHAR(16))

ALTER TABLE orders ALTER COLUMN order_type TYPE VARCHAR(32);
