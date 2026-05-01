-- Phase F: audit/recovery state for paper trading safety

ALTER TABLE orders ADD COLUMN IF NOT EXISTS intended_stop_loss NUMERIC(20,8) NOT NULL DEFAULT 0;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS intended_take_profit NUMERIC(20,8) NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS risk_decisions (
    id BIGSERIAL PRIMARY KEY,
    candidate_id BIGINT,
    cycle_id VARCHAR(64) NOT NULL,
    approved BOOLEAN NOT NULL DEFAULT false,
    final_position_notional NUMERIC(20,8) NOT NULL DEFAULT 0,
    required_margin NUMERIC(20,8) NOT NULL DEFAULT 0,
    estimated_loss NUMERIC(20,8) NOT NULL DEFAULT 0,
    reason_codes TEXT NOT NULL DEFAULT '[]',
    portfolio_rank INT NOT NULL DEFAULT 0,
    portfolio_reject_reason VARCHAR(64) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_risk_decisions_cycle ON risk_decisions(cycle_id);
CREATE INDEX IF NOT EXISTS idx_risk_decisions_candidate ON risk_decisions(candidate_id);

CREATE TABLE IF NOT EXISTS llm_usage_daily (
    usage_date DATE PRIMARY KEY,
    calls INT NOT NULL DEFAULT 0,
    cost_usd NUMERIC(20,8) NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
