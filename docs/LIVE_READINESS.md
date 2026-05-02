# Live Mode Readiness Criteria

## Go/No-Go for BOTSURV_MODE=LIVE

Before `BOTSURV_MODE=LIVE` is allowed, ALL of the following must be true. This is enforced at startup via `CheckLiveReadiness()`.

---

## Data Integrity (auto-checked)

These are verified by querying `decision_logs` and related tables:

| # | Criterion | Check | Source |
|---|-----------|-------|--------|
| 1 | Zero NaN/Inf reaching strategy layer in last 30 days | No `REJECTED_RISK` with reason "INVALID_ENTRY" or "INVALID_STOP_LOSS" | `decision_logs` |
| 2 | Zero candidate generated from incomplete candle in last 30 days | No `BLOCKED_HARD` with data_validation_result "blocks_triggered" | `decision_logs` |
| 3 | Zero position state mismatch in last 30 days | No `REJECTED_SAFETY` with failed_check "PositionReconciliation" | `decision_logs` |

---

## Pipeline Correctness (auto-checked)

| # | Criterion | Check | Source |
|---|-----------|-------|--------|
| 4 | 100% of decisions have full DecisionLog row | Every cycle has decisions logged; count matches candidates | `cycles` + `decision_logs` |
| 5 | 100% of paper trades have linked decision_id | No paper trades with NULL decision_id | `paper_trades` |
| 6 | All counterfactuals tracked to completion (no orphans) | No `status='tracking'` older than 48h | `candidate_outcomes` |

---

## Performance (paper mode, last 30 days)

| # | Criterion | Threshold | Source |
|---|-----------|-----------|--------|
| 7 | Minimum 200 paper trades | `count >= 200` | `paper_trades` |
| 8 | Profit factor > 1.2 | `gross_wins / gross_losses > 1.2` | `paper_trades` |
| 9 | Max drawdown <= 8% | Computed from equity curve | `paper_trades` |
| 10 | Max consecutive losses <= configured guard | Default: 5 (or from config) | `paper_trades` |
| 11 | Avg R per trade > 0 | `avg(r_multiple) > 0` | `paper_trades` |

---

## Operational (manual)

| # | Criterion | Verified by |
|---|-----------|-------------|
| 12 | Emergency stop tested manually | Team lead sign-off |
| 13 | BTC flash crash circuit breaker tested with simulated data | QA engineer sign-off |
| 14 | Position reconciliation tested by manually creating mismatch | QA engineer sign-off |

---

## Manual

| # | Criterion | Verified by |
|---|-----------|-------------|
| 15 | Live config reviewed by 2 team members | Sign-off document |
| 16 | Initial live capital agreed (start small) | Config review |
| 17 | Live mode requires `LIVE_CONFIRMED=yes` in env (not in default config) | Env check at startup |

---

## Startup Enforcement

When `BOTSURV_MODE=LIVE` and `LIVE_CONFIRMED=yes` are both set, the bot:
1. Runs `CheckLiveReadiness()` against the database
2. If any auto-checked criterion (1-11) fails: refuses to start, logs the failure
3. If all auto-checked criteria pass: starts in LIVE mode

Manual criteria (12-17) are logged as warnings but do not block startup.

---

## SQL Dashboard Queries

### 1. Win rate by strategy (last 30 days)
```sql
SELECT dl.final_action, COUNT(*), AVG((pt.pnl_net)) as avg_pnl
FROM decision_logs dl
JOIN paper_trades pt ON dl.decision_id = pt.decision_id
WHERE dl.timestamp >= NOW() - INTERVAL '30 days'
AND pt.closed_at IS NOT NULL
GROUP BY dl.final_action;
```

### 2. Counterfactual rejection accuracy
```sql
SELECT
  COUNT(*) as total_rejected,
  SUM(CASE WHEN would_have_outcome = 'sl_hit' THEN 1 ELSE 0 END) as correctly_rejected,
  SUM(CASE WHEN would_have_outcome IN ('tp1_hit', 'tp2_hit') THEN 1 ELSE 0 END) as would_have_won,
  ROUND(SUM(CASE WHEN would_have_outcome = 'sl_hit' THEN 1 ELSE 0 END)::numeric / NULLIF(COUNT(*), 0) * 100, 2) as accuracy_pct
FROM candidate_outcomes
WHERE status = 'completed';
```

### 3. Daily PnL and cumulative equity
```sql
SELECT
  DATE(closed_at) as trade_date,
  COUNT(*) as trades,
  SUM(pnl_net) as daily_pnl,
  SUM(SUM(pnl_net)) OVER (ORDER BY DATE(closed_at)) as cumulative_equity
FROM paper_trades
WHERE closed_at IS NOT NULL
GROUP BY DATE(closed_at)
ORDER BY trade_date;
```

### 4. R-multiple distribution
```sql
SELECT
  CASE
    WHEN r_multiple <= -2 THEN '<= -2R'
    WHEN r_multiple <= -1 THEN '-2R to -1R'
    WHEN r_multiple <= 0 THEN '-1R to 0R'
    WHEN r_multiple <= 1 THEN '0R to 1R'
    WHEN r_multiple <= 2 THEN '1R to 2R'
    ELSE '> 2R'
  END as r_bucket,
  COUNT(*) as count
FROM paper_trades
WHERE r_multiple IS NOT NULL AND closed_at IS NOT NULL
GROUP BY r_bucket
ORDER BY r_bucket;
```

### 5. Pipeline health: decisions by final_action (last 24h)
```sql
SELECT
  final_action,
  COUNT(*) as count,
  ROUND(COUNT(*)::numeric / SUM(COUNT(*)) OVER() * 100, 1) as pct
FROM decision_logs
WHERE timestamp >= NOW() - INTERVAL '24 hours'
GROUP BY final_action
ORDER BY count DESC;
```
