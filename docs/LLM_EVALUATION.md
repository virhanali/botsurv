# LLM Reviewer Evaluation

This document describes how to measure whether the LLM Reviewer adds value or is expensive theater.

## Evaluation Query

Run this SQL monthly to compare LLM reviewer decisions against paper trade outcomes:

```sql
-- Compare LLM reviewer agreement with backend decisions and outcomes
WITH decisions_30d AS (
  SELECT
    dl.*,
    pt.pnl_net,
    pt.exit_reason,
    pt.r_multiple,
    co.would_have_hit_tp1,
    co.would_have_hit_sl,
    co.would_have_outcome,
    co.result_in_r
  FROM decision_logs dl
  LEFT JOIN paper_trades pt ON pt.decision_id = dl.decision_id
  LEFT JOIN candidate_outcomes co ON co.decision_id = dl.decision_id
  WHERE dl.timestamp >= NOW() - INTERVAL '30 days'
    AND dl.llm_review IS NOT NULL
)
SELECT
  strategy_attempted,
  llm_mode_active,
  llm_review_action,
  COUNT(*) AS decisions,
  COUNT(CASE WHEN final_action = 'REJECTED_LLM_REVIEW' THEN 1 END) AS llm_rejected,
  COUNT(CASE WHEN final_action LIKE 'EXECUTED_%' THEN 1 END) AS executed,
  -- Would-have outcomes for candidates the LLM reviewed but didn't block
  COUNT(CASE WHEN would_have_hit_tp1 THEN 1 END) AS would_have_wins,
  COUNT(CASE WHEN would_have_hit_sl THEN 1 END) AS would_have_losses,
  AVG(result_in_r) AS avg_r,
  -- Paper trade outcomes
  AVG(pnl_net) AS avg_pnl_net,
  AVG(r_multiple) AS avg_r_multiple,
  COUNT(CASE WHEN pnl_net > 0 THEN 1 END) * 1.0 / NULLIF(COUNT(*), 0) AS win_rate
FROM decisions_30d
GROUP BY strategy_attempted, llm_mode_active, llm_review_action
ORDER BY llm_mode_active, decisions DESC;
```

## Key Questions

### 1. Does the LLM "would have rejected" candidates perform worse?
Compare in audit_only mode:
- Candidates the LLM flagged as REJECT vs candidates it APPROVED
- If REJECT-flagged candidates have lower avg_r, lower win_rate, or more would_have_losses → LLM has signal

### 2. Did veto mode skip trades that would have lost?
Compare veto mode vs off mode:
- In veto mode, LLM can REJECT
- If veto mode has higher win_rate without sacrificing too many winning trades → valuable

### 3. Did review mode improve trade quality?
Compare review mode vs off mode:
- Review mode can force retest-only entries
- If review mode has better avg_r or lower max_adverse_excursion → valuable

## What to look for

| Metric | Signal | Noise |
|--------|--------|-------|
| LLM rejected trades have worse outcomes | Promote to veto mode | Keep in audit_only |
| LLM approved trades have same outcomes as no-LLM | LLM adds no value | Keep at off |
| LLM invalid_response rate > 5% | Prompt needs fixing | Check prompt version |
| Daily cost exceeds $5 with no measurable edge | Reduce LLM scope | Consider cheaper model |
| LLM latency p95 > 3s | Tune timeout | Faster model or lower max_tokens |

## Monitoring Dashboard Items

- **LLM latency p50/p95**: Track via `llm_review` log events → `latency_ms`
- **Invalid rate**: `llm_invalid_response` counter / total calls
- **Daily cost**: Sum of `cost_usd` per day
- **Agreement rate**: % of decisions where LLM action == backend action
- **Mode distribution**: decisions grouped by `llm_mode_active`

## Measurement Protocol

1. Start with `LLM_MODE=audit_only` for at least 2 weeks
2. Collect data every week using the evaluation query
3. Present findings in a table: did LLM correctly identify bad trades?
4. Only promote to `veto` if LLM shows clear statistical edge
5. Never auto-promote modes - manual decision only
6. Do quarterly reviews even after promotion

## Cost-Benefit Analysis

```
Monthly LLM cost = daily_cost_usd × 30
Monthly edge = (Improved win_rate × avg_trade_size × trades_per_month) - LLM cost

If monthly edge < $0 for 2 consecutive months → set LLM_MODE=off
```

## Version Tracking

This document version: v1.0.0
Corresponding llm_prompt_version: v1.0.0
