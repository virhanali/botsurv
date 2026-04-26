-- Phase 1 initial schema

CREATE TABLE IF NOT EXISTS candles (
    id BIGSERIAL PRIMARY KEY,
    symbol VARCHAR(32) NOT NULL,
    timeframe VARCHAR(8) NOT NULL,
    open_time BIGINT NOT NULL,
    open NUMERIC(20,8) NOT NULL,
    high NUMERIC(20,8) NOT NULL,
    low NUMERIC(20,8) NOT NULL,
    close NUMERIC(20,8) NOT NULL,
    volume NUMERIC(20,8) NOT NULL,
    turn_over NUMERIC(20,8) NOT NULL DEFAULT 0,
    confirmed BOOLEAN NOT NULL DEFAULT false,
    UNIQUE(symbol, timeframe, open_time)
);

CREATE INDEX IF NOT EXISTS idx_candles_symbol_timeframe ON candles(symbol, timeframe, open_time);

CREATE TABLE IF NOT EXISTS universe_symbols (
    id BIGSERIAL PRIMARY KEY,
    symbol VARCHAR(32) NOT NULL UNIQUE,
    status VARCHAR(16) NOT NULL DEFAULT '',
    quote_asset VARCHAR(8) NOT NULL DEFAULT '',
    base_asset VARCHAR(16) NOT NULL DEFAULT '',
    min_notional NUMERIC(20,8) NOT NULL DEFAULT 0,
    tick_size NUMERIC(20,8) NOT NULL DEFAULT 0,
    lot_size NUMERIC(20,8) NOT NULL DEFAULT 0,
    max_leverage NUMERIC(20,8) NOT NULL DEFAULT 0,
    blacklist BOOLEAN NOT NULL DEFAULT false,
    force_include BOOLEAN NOT NULL DEFAULT false,
    last_scan_at TIMESTAMPTZ,
    liquidity_score NUMERIC(20,8) NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS cycles (
    id BIGSERIAL PRIMARY KEY,
    cycle_id VARCHAR(36) NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    ended_at TIMESTAMPTZ,
    status VARCHAR(16) NOT NULL,
    reason_codes TEXT NOT NULL DEFAULT '[]'
);

CREATE TABLE IF NOT EXISTS candidates (
    id BIGSERIAL PRIMARY KEY,
    cycle_id VARCHAR(36) NOT NULL,
    symbol VARCHAR(32) NOT NULL,
    candidate_score NUMERIC(20,8) NOT NULL DEFAULT 0,
    liquidity_score NUMERIC(20,8) NOT NULL DEFAULT 0,
    execution_score NUMERIC(20,8) NOT NULL DEFAULT 0,
    setup_score NUMERIC(20,8) NOT NULL DEFAULT 0,
    volatility_score NUMERIC(20,8) NOT NULL DEFAULT 0,
    llm_eligible BOOLEAN NOT NULL DEFAULT false,
    llm_routing_reason_codes TEXT NOT NULL DEFAULT '[]',
    regime VARCHAR(16) NOT NULL DEFAULT '',
    setup_type VARCHAR(32) NOT NULL DEFAULT '',
    side VARCHAR(8) NOT NULL DEFAULT '',
    entry_type VARCHAR(16) NOT NULL DEFAULT '',
    proposed_entry NUMERIC(20,8) NOT NULL DEFAULT 0,
    proposed_stop_loss NUMERIC(20,8) NOT NULL DEFAULT 0,
    proposed_take_profit NUMERIC(20,8) NOT NULL DEFAULT 0,
    stop_loss_pct NUMERIC(20,8) NOT NULL DEFAULT 0,
    take_profit_pct NUMERIC(20,8) NOT NULL DEFAULT 0,
    rr NUMERIC(20,8) NOT NULL DEFAULT 0,
    invalidation_level NUMERIC(20,8) NOT NULL DEFAULT 0,
    expected_move NUMERIC(20,8) NOT NULL DEFAULT 0,
    estimated_total_cost NUMERIC(20,8) NOT NULL DEFAULT 0,
    reason_codes TEXT NOT NULL DEFAULT '[]'
);

CREATE INDEX IF NOT EXISTS idx_candidates_cycle ON candidates(cycle_id);
