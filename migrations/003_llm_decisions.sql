-- Phase C: LLM decisions persistence

CREATE TABLE IF NOT EXISTS llm_decisions (
    id BIGSERIAL PRIMARY KEY,
    candidate_id BIGINT,
    cycle_id VARCHAR(36) NOT NULL,
    raw_response TEXT,
    decision VARCHAR(32),
    confidence NUMERIC(3,2),
    size_multiplier NUMERIC(3,2),
    regime VARCHAR(16),
    reason_codes TEXT DEFAULT '[]',
    risk_flags TEXT DEFAULT '[]',
    notes TEXT,
    validation_status VARCHAR(16),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
