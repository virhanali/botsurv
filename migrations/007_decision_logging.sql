-- Phase 5: Decision Logging, Counterfactual Tracking, Paper Trading
-- decision_logs: canonical record for every decision
-- candidate_outcomes: counterfactual price tracking
-- paper_trades: simulated paper mode trades
-- paper_account_state: persistent paper account across restarts

CREATE TABLE IF NOT EXISTS decision_logs (
    decision_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cycle_id VARCHAR(64) NOT NULL,
    timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    mode VARCHAR(16) NOT NULL DEFAULT 'SHADOW',
    symbol VARCHAR(32) NOT NULL,
    timeframe VARCHAR(8) NOT NULL,
    candle_count INT NOT NULL DEFAULT 0,
    data_validation_result VARCHAR(32) NOT NULL DEFAULT 'passed',
    indicator_snapshot JSONB,
    regime_snapshot JSONB,
    strategy_attempted JSONB,
    candidate JSONB,
    score_breakdown JSONB,
    near_misses JSONB,
    risk_validation JSONB,
    safety_validation JSONB,
    order_plan JSONB,
    llm_review JSONB,
    llm_mode_active BOOLEAN NOT NULL DEFAULT false,
    final_action VARCHAR(32) NOT NULL,
    final_action_reason TEXT NOT NULL DEFAULT '',
    engine_version VARCHAR(32) NOT NULL DEFAULT 'v2.1',
    scoring_version VARCHAR(32) NOT NULL DEFAULT '',
    risk_config_version VARCHAR(32) NOT NULL DEFAULT '',
    llm_prompt_version VARCHAR(32) NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_decision_logs_cycle ON decision_logs(cycle_id);
CREATE INDEX IF NOT EXISTS idx_decision_logs_symbol ON decision_logs(symbol);
CREATE INDEX IF NOT EXISTS idx_decision_logs_action ON decision_logs(final_action);
CREATE INDEX IF NOT EXISTS idx_decision_logs_timestamp ON decision_logs(timestamp);

CREATE TABLE IF NOT EXISTS candidate_outcomes (
    decision_id UUID NOT NULL REFERENCES decision_logs(decision_id),
    symbol VARCHAR(32) NOT NULL,
    side VARCHAR(8) NOT NULL,
    entry_price NUMERIC(20,8) NOT NULL,
    stop_loss NUMERIC(20,8) NOT NULL,
    take_profits JSONB,
    price_at_15m NUMERIC(20,8),
    price_at_1h NUMERIC(20,8),
    price_at_4h NUMERIC(20,8),
    price_at_24h NUMERIC(20,8),
    max_favorable_excursion_24h NUMERIC(20,8),
    max_adverse_excursion_24h NUMERIC(20,8),
    would_have_hit_tp1 BOOLEAN NOT NULL DEFAULT false,
    would_have_hit_sl BOOLEAN NOT NULL DEFAULT false,
    would_have_outcome VARCHAR(16) NOT NULL DEFAULT 'open',
    result_in_r NUMERIC(20,8) NOT NULL DEFAULT 0,
    tracked_until TIMESTAMPTZ,
    status VARCHAR(16) NOT NULL DEFAULT 'tracking',
    PRIMARY KEY (decision_id)
);
CREATE INDEX IF NOT EXISTS idx_candidate_outcomes_status ON candidate_outcomes(status);
CREATE INDEX IF NOT EXISTS idx_candidate_outcomes_symbol ON candidate_outcomes(symbol);

CREATE TABLE IF NOT EXISTS paper_trades (
    paper_trade_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    decision_id UUID NOT NULL REFERENCES decision_logs(decision_id),
    opened_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at TIMESTAMPTZ,
    symbol VARCHAR(32) NOT NULL,
    side VARCHAR(8) NOT NULL,
    qty NUMERIC(20,8) NOT NULL,
    leverage NUMERIC(20,8) NOT NULL DEFAULT 1,
    entry_price NUMERIC(20,8) NOT NULL,
    exit_price NUMERIC(20,8),
    stop_loss NUMERIC(20,8) NOT NULL DEFAULT 0,
    take_profit NUMERIC(20,8) NOT NULL DEFAULT 0,
    fees_paid NUMERIC(20,8) NOT NULL DEFAULT 0,
    funding_paid NUMERIC(20,8) NOT NULL DEFAULT 0,
    pnl_gross NUMERIC(20,8),
    pnl_net NUMERIC(20,8),
    r_multiple NUMERIC(20,8),
    exit_reason VARCHAR(16) NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_paper_trades_decision ON paper_trades(decision_id);
CREATE INDEX IF NOT EXISTS idx_paper_trades_opened ON paper_trades(opened_at);
CREATE INDEX IF NOT EXISTS idx_paper_trades_closed_at ON paper_trades(closed_at);

CREATE TABLE IF NOT EXISTS paper_account_state (
    id INT PRIMARY KEY DEFAULT 1,
    starting_equity NUMERIC(20,8) NOT NULL DEFAULT 0,
    current_equity NUMERIC(20,8) NOT NULL DEFAULT 0,
    total_trades INT NOT NULL DEFAULT 0,
    wins INT NOT NULL DEFAULT 0,
    losses INT NOT NULL DEFAULT 0,
    realized_pnl NUMERIC(20,8) NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Initialize the singleton paper account row if it doesn't exist
INSERT INTO paper_account_state (id, starting_equity, current_equity) VALUES (1, 0, 0)
ON CONFLICT (id) DO NOTHING;
