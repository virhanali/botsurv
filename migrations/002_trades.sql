-- Phase B: trades persistence (positions, orders, executions, account snapshots)

CREATE TABLE IF NOT EXISTS positions (
    id BIGSERIAL PRIMARY KEY,
    symbol VARCHAR(32) NOT NULL,
    side VARCHAR(8) NOT NULL,
    entry_price NUMERIC(20,8) NOT NULL,
    size NUMERIC(20,8) NOT NULL,
    leverage NUMERIC(5,2) NOT NULL,
    margin NUMERIC(20,8) NOT NULL,
    stop_loss NUMERIC(20,8) NOT NULL,
    take_profit NUMERIC(20,8),
    unrealized_pnl NUMERIC(20,8) DEFAULT 0,
    realized_pnl NUMERIC(20,8) DEFAULT 0,
    status VARCHAR(16) NOT NULL DEFAULT 'open',
    source VARCHAR(32) DEFAULT 'internal_strategy',
    opened_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_positions_symbol ON positions(symbol);
CREATE INDEX IF NOT EXISTS idx_positions_status ON positions(status);

CREATE TABLE IF NOT EXISTS orders (
    id BIGSERIAL PRIMARY KEY,
    broker_order_id VARCHAR(64),
    position_id BIGINT,
    symbol VARCHAR(32) NOT NULL,
    side VARCHAR(8) NOT NULL,
    order_type VARCHAR(16) NOT NULL,
    qty NUMERIC(20,8) NOT NULL,
    price NUMERIC(20,8),
    stop_price NUMERIC(20,8),
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_orders_symbol ON orders(symbol);
CREATE INDEX IF NOT EXISTS idx_orders_status ON orders(status);

CREATE TABLE IF NOT EXISTS executions (
    id BIGSERIAL PRIMARY KEY,
    order_id BIGINT NOT NULL,
    symbol VARCHAR(32) NOT NULL,
    side VARCHAR(8) NOT NULL,
    qty NUMERIC(20,8) NOT NULL,
    price NUMERIC(20,8) NOT NULL,
    fee NUMERIC(20,8) DEFAULT 0,
    slippage NUMERIC(20,8) DEFAULT 0,
    executed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_executions_order ON executions(order_id);

CREATE TABLE IF NOT EXISTS account_snapshots (
    id BIGSERIAL PRIMARY KEY,
    balance NUMERIC(20,8) NOT NULL,
    available_balance NUMERIC(20,8) NOT NULL,
    used_margin NUMERIC(20,8) NOT NULL,
    equity NUMERIC(20,8) NOT NULL,
    realized_pnl NUMERIC(20,8) NOT NULL,
    unrealized_pnl NUMERIC(20,8) NOT NULL,
    total_fees NUMERIC(20,8) NOT NULL,
    total_slippage NUMERIC(20,8) NOT NULL,
    daily_loss NUMERIC(20,8) NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_account_snapshots_time ON account_snapshots(recorded_at);
