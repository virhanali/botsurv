-- Phase G: widen VARCHAR(16) columns that overflow for longer symbols/statuses

ALTER TABLE universe_symbols ALTER COLUMN status TYPE VARCHAR(32);
ALTER TABLE universe_symbols ALTER COLUMN base_asset TYPE VARCHAR(32);

ALTER TABLE cycles ALTER COLUMN status TYPE VARCHAR(32);

ALTER TABLE candidates ALTER COLUMN regime TYPE VARCHAR(32);
ALTER TABLE candidates ALTER COLUMN entry_type TYPE VARCHAR(32);

ALTER TABLE llm_decisions ALTER COLUMN regime TYPE VARCHAR(32);
ALTER TABLE llm_decisions ALTER COLUMN validation_status TYPE VARCHAR(32);

ALTER TABLE positions ALTER COLUMN status TYPE VARCHAR(32);

ALTER TABLE candidate_outcomes ALTER COLUMN would_have_outcome TYPE VARCHAR(32);
ALTER TABLE candidate_outcomes ALTER COLUMN status TYPE VARCHAR(32);

ALTER TABLE paper_trades ALTER COLUMN exit_reason TYPE VARCHAR(32);

ALTER TABLE decision_logs ALTER COLUMN mode TYPE VARCHAR(32);
