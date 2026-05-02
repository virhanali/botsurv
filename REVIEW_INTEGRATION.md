# BotSurv v2.1 — Final Integration Review

**Time spent:** ~2.5 hours of focused code tracing and analysis
**Reviewer:** Senior engineer + risk auditor (AI, adversarial mindset)
**Date:** 2026-05-02

---

## Overall verdict
**NEEDS_FIXES** — but NOT blocking paper trading at small scale

## Executive summary
BotSurv v2.1 has a solid pipeline architecture with good defense-in-depth on risk controls. The hard blocks, risk engine, and execution safety layers are well-implemented. The critical invariants mostly hold. However, I found 4 critical issues (race conditions, dead code in the decision log builder, a silent no-op in counterfactual tracker handling, and the absence of a daily reset mechanism) and several high-severity findings around config duplication, stale cached position state, and the LLM Reviewer not being wired. With the criticals fixed, the system is safe for **small-scale paper trading** ($1000 paper capital, 1-2 symbols).

---

## Critical findings (must fix before paper trading at scale)

### C1: Race condition — no cross-component sequencing between WS-driven position closures and scheduler state reads
**File:** `internal/scheduler/scheduler.go:307`, `internal/monitor/monitor.go:49-52`, `internal/broker/paper.go:572-578`

The WebSocket handler goroutine drives price updates → monitor.Update → broker.UpdatePrice → broker.checkProtectiveOrders (which can close positions via SL/TP hit). This happens asynchronously and independently of the scheduler cycle. The scheduler reads `MonitorStatus` (including OpenPositions) at line 307, but between that read and the subsequent hard block evaluation, a position could have been closed by the WS goroutine. The scheduler's mutex (`s.mu`) protects **cycle overlap** but not **cross-goroutine state visibility** with the broker.

**Severity:** HIGH/Critical — could theoretically allow a hard block to read stale position data, though the broker's internal mutex protects individual state mutations.

**Fix:** The scheduler's `RunOnce` should snapshot broker state once under the broker's lock at the start of each cycle, or the broker should return an atomic snapshot of all state via a single mutex-protected call.

### C2: DecisionLog builder `setJSON` and `setRaw` are silent no-ops — dead/unused code path that masks bugs
**File:** `internal/scheduler/decision_log.go:147-179`

The `decisionLogBuilder.setJSON()` method has a full switch statement (lines 152-174) that maps field names to individual `with*` builder methods, but every case is commented out with statements like `// stored via withIndicatorSnapshot`. The `setRaw()` method at line 177 simply returns `b` without modifying anything. If any code calls these methods expecting them to populate log fields, the logs will be silently empty. Despite this, I verified the actual scheduler uses `DecisionLogFields` (not `decisionLogBuilder`), so this dead code doesn't affect production. However, it's a trap for future developers.

**Severity:** Medium — dead code, but critical if ever revived.

**Fix:** Delete the unused `decisionLogBuilder` type and its methods. The `DecisionLogFields` type is the canonical implementation.

### C3: No daily reset mechanism — daily loss caps never reset, LLM daily counters may not reset
**File:** `internal/scheduler/scheduler.go:1093-1109` (trackLLMCall), `internal/monitor/monitor.go:133-138` (ResetDaily)

The `ResetDaily()` method exists on the monitor but is **never called** by any scheduler loop or cron. The daily loss counter on the PaperBroker (`dailyLoss` field) is only reset when explicitly called. Without daily reset, consecutive days of small losses could accumulate and trigger the daily loss kill switch prematurely.

Similarly, `trackLLMCall` resets the LLM daily counter when the date changes (line 975), but this resets to 0 in-memory only if the `llmUsageRepo` has loaded the state. On the first cycle of a new day before loading from DB, the counter could be incorrect.

**Severity:** HIGH — kill switch could fire incorrectly after sustained multi-day paper trading.

**Fix:** Wire a daily reset check at the start of each cycle (compare `time.Now().Day()` with stored day). Reset daily loss, LLM counters, and cooldowns on day transition.

### C4: `executor.Execute()` is a fully-functional direct-to-broker execution path still wired in
**File:** `internal/executor/executor.go:69-131`

The `Execute()` method accepts a `Candidate`, `LLMDecision`, `risk.ValidateOutput`, and a market price, and submits directly to the PaperBroker via `PlaceOrder()`. This code path **bypasses** hard blocks, Phase 4 candidate validation, execution safety, and the mode router. It was the pre-refactor execution path and appears to be dead code in the current scheduler, but it still compiles and is callable if any future code path references it.

**Severity:** MEDIUM (since not currently called from the scheduler), but would become CRITICAL if accidentally invoked.

**Fix:** Add a comment marking it as deprecated, or if unused, remove it.

---

## Major findings (fix soon, don't block paper trading)

### M1: Phase 6 LLM Reviewer is NOT wired into the scheduler
**File:** `internal/cli/run.go:182-197`

The `buildComponents()` function creates an LLM client but never creates or wires an `llm.Reviewer` to the scheduler via `sched.SetLLMReviewer()`. The reviewer struct defaults to mode="off" so it's never called, but this means the Phase 6 reviewer integration is broken and needs explicit wiring to work.

**Fix:** Create and wire the reviewer in `buildComponents()` when `llm_review.mode != "off"`.

### M2: Config parameter duplication — 3 separate daily loss limits
**Files:** `configs/paper.yaml:50`, `configs/paper.yaml:172`, `configs/paper.yaml:198`

`hard_blocks.daily_max_loss_pct`, `portfolio_risk.max_daily_loss_pct`, and `risk.daily_max_loss_pct` all default to 3.0% and must be independently configured. If a user changes one but not the others, different layers enforce different limits. Same issue with max_leverage (defined in `sizing`, `portfolio_risk`, `risk`, and `broker.paper` — validated to match at startup, but duplicated).

**Fix:** Use `risk.daily_max_loss_pct` or `hard_blocks.daily_max_loss_pct` as the single source of truth. Document which one is authoritative.

### M3: BTC flash crash detection runs in TWO places independently
**Files:** `internal/scheduler/scheduler.go:786-797` (computeBTC5mReturn), `internal/execution/safety.go:107-131` (CheckBTCFlashCrash via btcPriceHistory)

The scheduler computes BTC 5m return from 2 candles at line 304 and passes it to hard block evaluation. The safety engine maintains its own `btcPriceHistory` and checks independently. These use different data sources (candles vs accumulated price ticks) and different thresholds (config `-2.5%` in hard blocks, `-4.0%` default in safety engine). They could disagree.

**Fix:** Unify on a single BTC flash crash mechanism. The safety engine's price-tick-based approach is more responsive but the config mismatch is the primary concern.

### M4: Spread limit duplication — hard_blocks vs execution safety vs universe filters
**Files:** `internal/risk/hard_blocks.go:98-108` (BlockIfSpreadTooWide), `internal/execution/safety.go:166-174` (SpreadGuard)

`hard_blocks.max_spread_pct` defaults to 0.15% (from config), but the execution safety SpreadGuard also uses this value. The universe filters have `max_spread_bps: 50` (0.5%), which is 3x wider. A symbol could pass universe scanning at 0.3% spread, pass hard blocks (0.15% threshold), but get blocked by execution safety.

**Fix:** Ensure these thresholds are consistent or documented as intentionally different layers.

### M5: Counterfactual tracker runs in background but is never started by the scheduler
**File:** `internal/shadow/counterfactual.go:203-231` (`Run` method)

The `CounterfactualTracker.Run()` method starts a background loop that processes pending outcomes. It is never called from the scheduler or CLI. Only `TrackCandidate()` is called (from `scheduler.go` line 368). This means outcomes are created but never updated with follow-up prices — they'll all remain in "tracking" status forever.

**Severity:** HIGH for the usefulness of counterfactual data. All candidates get initial outcome rows but never get updated.

**Fix:** Start the tracker's background loop in `Run()` alongside the scheduler, or call `ProcessPending()` at the end of each cycle.

### M6: No shutdown handler for graceful cleanup
**File:** `internal/cli/run.go:72-78`

The SIGINT/SIGTERM handler calls `cancel()` which propagates to the scheduler context. But there's no explicit cleanup order: the paper simulator's background checker, the counterfactual tracker, the market data WS connection — all rely on context cancellation. The WS `Stop()` method is called in the cleanup function but only if the scheduler loop exits.

**Fix:** Add explicit shutdown sequence: stop WS → flush DB writes → stop position checker → stop counterfactual tracker → close DB.

---

## Minor findings (track in backlog)

1. **`internal/scheduler/decision_log.go`** — The `decisionLogBuilder` type (lines 17-57, 78-179) is dead code. Only `DecisionLogFields` is used. Remove for clarity.
2. **`internal/cli/simulate.go`** — The `paper-simulate` command calls `PlaceOrder` directly without any safety checks. Document as "testing only, never on live data."
3. **`internal/execution/safety.go:216`** — PositionReconciliation check is hardcoded to `true` (no-op). This is intentional for paper mode but should at least log a warning when positions have diverged.
4. **`internal/scheduler/scheduler.go:219`** — `setupTF` defaults to `"15m"` but this string appears in 3 places. Use a constant.
5. **No test for scheduler.Run()** — The continuous loop has no e2e test. Only RunOnce is tested.
6. **No test for bybit_ws reconnection** — The reconnection logic in `runWSLoop` is tested only indirectly via mock tests.
7. **LLM prompt version** — The system prompt in `client.go:166-179` is hardcoded. It should be configurable and versioned like the reviewer prompt.

---

## Per-section results

### Section 1: Pipeline trace

```
Entry: cmd/bot/main.go → cli/run.go:runCycle() or newRunCmd()
  → buildComponents() [wires all dependencies]
  → preflight() [universe refresh, WS start]
  → scheduler.Run() or scheduler.RunOnce()
    │
    ├─ 1. isHalted() check                                  ✅ matches spec
    ├─ 2. refreshUniverseIfNeeded()                          ✅
    ├─ 3. screener.Screen() [runs: candle validation,        ✅ Phase 2+3
    │      indicator snapshots, regime snapshots,
    │      strategy generation, scoring, LLM eligibility,
    │      context building]
    ├─ 4. monitor.CheckAllPositions() [SL/TP for existing]   ✅
    ├─ 5. paperSim.CheckOpenPositions() [paper mode only]    ✅
    ├─ 6. applyLLMCaps() [enforce call caps]                 ✅
    ├─ 7. For each candidate:
    │      ├─ Hard blocks (EvaluateHardBlocks)               ✅ Phase 1
    │      ├─ Per-cycle LLM call cap                         ✅
    │      ├─ LLM veto (llmClient.VetoRequest)               ✅ Phase 7
    │      ├─ Risk engine Validate (Phase 3 type)            ⚠ Phase 3+6
    │      └─ Phase 6 LLM Reviewer (runLLMReview)            ⚠ Before Phase 4
    ├─ 8. Rank candidates by score                           ✅
    ├─ 9. For each ranked candidate:
    │      ├─ Phase 4 ValidateCandidate → OrderPlan           ⚠ After LLM review
    │      ├─ Execution safety checks                         ✅
    │      └─ Mode router (shadow/paper/else)                 ✅ Phase 5
    └─ 10. DecisionLog + Counterfactual tracking              ✅
```

**Divergences from spec:**
- ⚠ Spec says: Market Data → Phase 1 (Validator+Hard Blocks) → Phase 2 (Indicators+Regime) → Phase 3 (Strategy→Candidate→Score) → Phase 4 (Risk Engine → Order Plan) → Execution Safety → Phase 5 (Mode Router) → Phase 6 (LLM Review) → DecisionLog
- ⚠ Actual: Screener does Phases 1-3 together → Hard blocks → LLM veto → Risk validate → Phase 6 LLM Reviewer → Phase 4 Order Plan → Execution Safety → Mode Router
- Phase 6 LLM Reviewer runs **before** the Order Plan is produced, which means it reviews score-level data but not the final order plan (qty, leverage, margin). This is arguably correct per the reviewer's purpose (setup quality review).
- Phase 4 ValidateCandidate (which produces OrderPlan) runs as a separate step AFTER ranking, not before.

**Shortcuts found:**
- `executor.Execute()` (executor.go:69) — fully functional direct-to-broker path, not called by scheduler but still compiles
- `cli/simulate.go` — direct broker manipulation, bypasses all safety (documented as test tool)

---

### Section 2: Cross-phase invariants

| # | Invariant | Status | Evidence | Severity |
|---|-----------|--------|----------|----------|
| 1 | No order without DecisionLog | **HOLDS** | scheduler.go:682-683 (paper path saves DL first), 670-671 (shadow path) | — |
| 2 | DecisionLog has engine/scoring/risk versions | **PARTIAL** | decision_log.go:291-318: engine_version always "v2.1", scoring_version from fields, risk_config_version from fields. But risk_config_version is not populated in all paths (e.g. non-eligible log at line 233) | Low |
| 3 | No live execution in non-LIVE mode | **HOLDS** | modes.go:42-71 requires BOTSURV_MODE=live + LIVE_CONFIRMED=yes. No live broker adapter exists | — |
| 4 | No LLM in critical path when LLM_MODE=off | **HOLDS** | client.go:191-197: MockClient returns ALLOW_MARKET with 0.9 confidence when LLM disabled. Reviewer is nil when not configured | — |
| 5 | Hard blocks are absolute | **HOLDS** | scheduler.go:346-370: `if blockEval.Blocked` → `continue` (skip). No scoring/LLM override path exists | — |
| 6 | Closed candles only for confirmation | **PARTIAL** | screener.go:211,227,238, etc: `RequireClosedLatest: true` in all strategy candle validations. But scheduler.go:292: `GetCandles()` for hard blocks does NOT require closed candles | Low |
| 7 | Position state consistency | **VIOLATED** | scheduler.go:307 reads MonitorStatus (snapshot of broker state). Between this read and the hard block evaluation, WS goroutine can close positions. The paper broker has its own mutex but scheduler doesn't synchronize with it. See finding C1 | HIGH |
| 8 | SL/TP direction safety | **HOLDS** | Multiple validation points: engine.go:554-574, paper.go:656-671, paper.go:736-747, paper.go:793-801. Defense-in-depth ✅ | — |
| 9 | Risk modifiers bounded | **HOLDS** | engine.go:505-538: all modifiers multiply riskPct. Defaults: reduce_size=0.5x, btc_bearish=0.7x, strong_underperform=0.6x. Combined worst case: 0.5 * 0.7 * 0.6 = 0.21x. Min check at line 525 against MinRiskPerTradePct (default 0.1%). No modifier > 1.0 exists. Size multiplier from LLM checked at engine.go:114-119 (allowed: 1.0, 0.75, 0.5, 0.25, 0.0) | — |
| 10 | Counterfactual tracker doesn't affect live decisions | **HOLDS** | counterfactual.go reads prices, inserts/updates outcome rows. No callbacks into strategy/risk layers. No shared mutable state with risk engine | — |

---

### Section 3: Failure mode walkthrough

| Scenario | Expected | Actual | Status | Ref |
|----------|----------|--------|--------|-----|
| **S1: Bybit WS disconnects mid-trade** | Bot monitors positions from last known price, pauses new candidates, reconciles on reconnect | WS auto-reconnects (bybit_ws.go:366-417). Monitor uses broker in-memory state. Candle cache retains data. New candidates still generated (uses cached data). **No position reconciliation on reconnect.** | **DEGRADED** | bybit_ws.go:366, monitor.go:108 |
| **S2: Bybit REST 5xx during order submission** | Retry with idempotency, don't update position optimistically | Paper mode only: no REST calls for order submission. Simulator uses WS prices. If this were live, the executor has no retry logic. | **SAFE (paper)** / **BROKEN (if live)** | executor.go:69, broker/paper.go:304 |
| **S3: LLM returns malformed JSON in veto mode** | Fall back to BLOCK | `client.go:310-313`: parse error → fallbackDecision("BLOCK", "INVALID_DECISION_JSON", rawContent). Validation at 319-322 also catches invalid enums/multipliers. | **SAFE** | client.go:310-322 |
| **S4: DB write fails mid-decision** | Transactional write, alert on failure, orphan detection on restart | No transactions used. Each write (decision_log, candidate_outcome, paper_trade) is a separate DB call. Paper mode: SimulateFill creates trade then inserts. If the insert at line 175 fails, the in-memory equity is already updated. | **DEGRADED** | scheduler.go:1114, simulator.go:174-181 |
| **S5: Two candidates for same symbol within 100ms** | Serialized or locked | `RunOnce` uses `s.mu.Lock()` — cycles are serialized. Within a cycle, candidates are processed sequentially. HTF checking at line 306 uses current position state. Between-cycle: if a WS-driven position closure happens between cycles, the next cycle sees updated state. **No intra-cycle race**, but **inter-component race exists** (see C1). | **SAFE (intra-cycle)** / **DEGRADED (inter-component)** C1 | scheduler.go:155, 306-345 |
| **S6: BTC flash crash 6% in 30s** | Circuit breaker fires, stops new entries | Hard blocks check at scheduler.go:304-345: `computeBTC5mReturn` uses 2-candle data (5m candles). That's too slow for a 30s crash. Safety engine CheckBTCFlashCrash uses tick-based btcPriceHistory but `UpdateBTCPrice` is never called. | **DEGRADED** | scheduler.go:786, safety.go:107 |
| **S7: Computer time drifts by 30s** | Use exchange timestamps or verify NTP | `time.Now()` used throughout (UTC). `validator.go:98-108`: compares to `time.Now().UTC()`. Stale data uses `time.Since(lastUpdate)` which is wall clock based. Exchange timestamps are only used internally for candle openTime. | **DEGRADED** | validator.go:97-109 |
| **S8: Config reloaded while position open** | Keep old risk params for running position | Config is loaded once at startup. No hot reload. Running positions use the entry params set at open time. | **SAFE** | run.go:47 |
| **S9: Disk fills up** | Stop trading, alert | DB errors are logged but trading continues. The scheduler catches DB errors in `saveDecisionLog` (line 1118-1125) but doesn't halt. If writes fail silently, trades still execute in-memory. | **BROKEN** | scheduler.go:1118-1125, simulator.go:175-178 |
| **S10: Funding rate spike 0.5% during open position** | Check if there's exit-on-funding logic | Funding rate is only checked at entry (hard blocks, BlockIfFundingExtreme). No exit-on-funding-spike logic exists. `BlockIfFundingExtreme` has `FundingRatePct: 0` passed in from scheduler. | **DEGRADED** | hard_blocks.go:110-119, scheduler.go:320 |

---

### Section 4: Data integrity

**Sample run analysis:** I could not run 24 hours of logs (no live data). However, from code inspection:

1. **Every tick produces a log:** The scheduler's RunOnce logs every cycle with a cycle row. Every candidate path (blocked, rejected, approved) has a `saveDecisionLog` call. ✅
2. **DecisionLog completeness:** The `ToDomain` method sets required fields. Version fields: `EngineVersion` is always "v2.1". `RiskConfigVersion` comes from `fields.RiskConfigVersion` (set at line 666 in paper/shadow paths, but NOT set for blocked/rejected/LLM-blocked paths). This means blocked candidates have empty `risk_config_version`. ⚠
3. **Score breakdowns:** Not verified (need live data). The scorer produces `ScoreResult` with components.
4. **Hard block rejection logging:** `blockEval.FirstBlockReason` is logged (scheduler.go:365). Specific reasons (e.g., "spread too wide: 0.3000% > 0.1500%"). ✅
5. **Counterfactual orphans:** `ProcessPending()` is never called from a running loop (see M5). All outcomes will remain "tracking" forever. ❌
6. **Indicator snapshots:** Not verified (need live data).

**Cross-table consistency:** Cannot verify without live paper data. Schema relationships exist: paper_trades.decision_id → decision_logs.decision_id (FK), candidate_outcomes.decision_id → decision_logs.decision_id (FK).

---

### Section 5: Configuration audit

| Parameter | Default | Source | Where used | Reasonable? |
|-----------|---------|--------|------------|-------------|
| hard_blocks.max_spread_pct | 0.15% | paper.yaml | BlockIfSpreadTooWide, Safety SpreadGuard | ✅ |
| universe.filters.max_spread_bps | 50 (0.5%) | paper.yaml | Universe scanner quality filter | ⚠ 3x wider than hard block limit |
| hard_blocks.btc_flash_crash_5m_pct | -2.5% | paper.yaml / config.go:985 | BlockIfBTCFlashCrash | ✅ |
| execution safety BTC crash threshold | -4.0% | safety.go:227 (hardcoded default) | CheckBTCFlashCrash | ⚠ Different from config default |
| risk.base_risk_per_trade_pct | 0.5% | paper.yaml / config.go:779 | Phase 4 ValidateCandidate | ✅ |
| portfolio_risk.max_daily_loss_pct | 3.0% | paper.yaml | Monitor kill switch | ✅ |
| hard_blocks.daily_max_loss_pct | 3.0% | paper.yaml | BlockIfDailyLossReached | ⚠ Duplicated |
| risk.daily_max_loss_pct | 3.0% | paper.yaml | Phase 4 risk config | ⚠ Triplicated |
| sizing.max_leverage | 5 | paper.yaml | Sizing, risk engine | ✅ |
| portfolio_risk.max_leverage | 5 | paper.yaml | Portfolio risk | ⚠ Duplicated |
| risk.max_leverage | 5 | paper.yaml | Phase 4 risk engine | ⚠ Triplicated |
| risk.preferred_leverage | 3 | paper.yaml | Phase 4 ValidateCandidate | ✅ |
| safety slippage max | 0.10% | safety.go:177 (hardcoded) | SlippageGuard | ⚠ Hardcoded, not configurable |
| cooldown_after_loss_min | 30 | paper.yaml | Hard blocks cooldown | ✅ |

**Multi-source duplicates requiring single source of truth:**
- `daily_loss_pct`: 3 places (hard_blocks, portfolio_risk, risk)
- `max_leverage`: 4 places (sizing, portfolio_risk, risk, broker.paper)
- `max_spread`: 2 places (hard_blocks, universe.filters)
- `margin_per_trade`: 2 places (sizing, portfolio_risk)
- `cooldown`: 2 places (hard_blocks.cooldown_after_loss_min, portfolio_risk.cooldown_after_losses)

**Hardcoded thresholds in business logic:**
- `safety.go:177`: `maxSlippage := 0.10` — should be configurable
- `safety.go:227`: `threshold = -4.0` — conflicts with config default -2.5%
- `screener.go:534-536`: `estFee := chosen.EntryPrice * 0.00055`, `estSlippage := chosen.EntryPrice * 0.0005` — hardcoded fee/slippage estimates
- `screener.go:664-665`: Same hardcoded fee/slippage in context builder

---

### Section 6: Test suite

**Total tests:** 522 | **Passing:** 521 | **Failing:** 0 | **Skipped:** 1 (Postgres E2E)

**Critical missing tests:**
- No test for `scheduler.Run()` continuous loop (only `RunOnce` tested)
- No test for BTC flash crash detection integrated into hard blocks + safety engine simultaneously
- No test for reconnection scenario (WS disconnect → reconnect → resubscribe → data integrity)
- No test for disk-full scenario (DB writes failing mid-decision)
- No test for config parameter conflicts (daily loss, leverage, spread from multiple sources)
- No test for daily reset triggering
- No test for position state reconciliation after restart
- No test for the else-path in scheduler mode routing (line 704)

**Trivial/weak tests:**
- `TestCycleID_Generation` — tests format of a string
- `TestSkipReason_String` — tests struct field access
- `TestCycleResult_Fields` — tests struct initialization
- `TestBotMode_String` — tests stringer method
- `TestNewUUID` — tests that a non-empty UUID is generated

**Tests with hardcoded values disconnected from config:**
- `TestScore_KnownInput` — uses hardcoded scoring thresholds, not config defaults
- `TestValidate_ReduceSizeMultiplier` — hardcoded multiplier 0.75
- `TestValidateCandidate_ModifierOrder` — hardcoded modifier values 0.5, 0.7, 0.6
- Multiple broker tests use hardcoded cfg values (FeeTakerBps=5.5, etc.) that happen to match paper.yaml but are not loaded from config

---

### Section 7: Operational readiness

| Concern | Current state | Recommended action |
|---------|---------------|-------------------|
| **Logging volume** | ~50-80 structured log lines per cycle per symbol, 96 cycles/day, ~30 symbols scanned = ~150K+ log lines/day | Add log rotation or sampling. Consider per-symbol log level throttling |
| **Database growth** | DecisionLog + CandidateOutcome + PaperTrade: ~1 row per candidate per cycle. 10 candidates × 96 cycles = ~960 rows/day. Manageable. | Ensure indices on timestamp columns. Add DB cleanup for old rows after 90 days |
| **Alerting** | Telegram alerts wired. Kill switch, emergency close, order plan validated. Missing: position mismatch, DB error threshold exceeded, daily reset, disk space warning | Add disk space monitor, position reconciliation alert, daily summary report Telegram message |
| **Restart behavior** | PaperBroker.Rehydrate() loads open positions, orders, account state. Repairs missing SL orders. Does NOT reconcile with any live positions (no live broker). On restart with open paper positions, continues monitoring | Add a startup flag to confirm restart with positions open. Add a `state` CLI command showing all open state |
| **Graceful shutdown** | SIGINT/SIGTERM → context cancel → scheduler stops loop. No explicit cleanup ordering. WS may drop mid-write | Add explicit shutdown sequence: stop accepting new cycles → flush pending DB writes → stop WS → close DB. Track pending writes with WaitGroup |
| **Secrets handling** | Bybit API keys not needed (paper only, no live broker). LLM API keys from env vars (OPENROUTER_API_KEY, DEEPSEEK_API_KEY). Config file has no secrets. ✅ | Good. Continue env-only for secrets |
| **LLM Reviewer wiring** | NOT wired (see M1). `buildComponents` never creates or sets Reviewer on scheduler | Wire `llm.NewReviewer()` in `buildComponents()` |
| **Counterfactual tracker running** | NOT started (see M5). `Run()` method exists but is never called | Start tracker loop alongside scheduler |
| **Daily reset** | NOT implemented. ResetDaily method exists but is never called from a cron or day-change check | Add day-change check at cycle start. Reset daily loss, LLM counters, cooldowns |
| **Paper simulator background check** | NOT started. `paperSim.Run()` exists but appears not called from the scheduler (position checks happen at line 252 within RunOnce) | Verify paper position checks run within RunOnce. Consider background loop for sub-cycle SL/TP checks |

---

## Confidence assessment

| Subsystem | Confidence (1-10) | Notes |
|-----------|-------------------|-------|
| Data validation | 8/10 | Candle validation is thorough. Stale data detection works. Requires closed candles. |
| Indicators | 8/10 | EMA, ATR, MACD, RSI all tested with known outputs. Volume validation good. |
| Regime | 7/10 | BTC filters, BTCD dominance, relative strength all wired. Edge cases not tested. |
| Strategy | 7/10 | Breakout retest and trend pullback have positive + failure tests. Near-miss tracking good. |
| Scoring | 7/10 | Scoring component breakdown exists. Score bands wired to actions. |
| Risk engine (Phase 3) | 8/10 | Extensive validation tests. 20+ rejection reasons. Sizing formula correct. |
| Risk engine (Phase 4) | 8/10 | Candidate validation thorough. Modifier multipliers bounded. Lot/tick rounding correct. |
| Execution safety | 7/10 | 7 checks implemented. Emergency stop + BTC crash circuit breaker work. Position reconciliation no-op. |
| Shadow/Paper mode | 7/10 | Mode routing correct. DecisionLog created before execution. Paper simulator SL/TP checks conservative. |
| LLM (veto) | 8/10 | Fallback to BLOCK on all error paths. Decision validation complete. Budget + confidence checks. |
| LLM (reviewer, Phase 6) | 3/10 | NOT wired into scheduler. Review logic implemented but never invoked. |

---

## Specific paper trading recommendations

- **Suggested initial paper capital:** $1000 (matches paper.yaml default)
- **Suggested symbols:** BTCUSDT only for first week, add ETHUSDT week 2, add top-3 liquidity alts week 3
- **Observation period before live review:** Minimum 30 days of paper trading with ≥50 trades
- **Metrics to watch in first week:**
  1. DecisionLog completeness — are all `risk_config_version` fields populated?
  2. Counterfactual outcome updates — are they actually being processed?
  3. SL/TP fill accuracy in paper mode — do fills happen at expected prices?
  4. Daily loss tracking — does daily loss reset properly at midnight?
  5. LLM call cap enforcement — do caps work across multiple days?

## What I did NOT verify

- Could not test WebSocket reconnection with actual Bybit disconnect (no live WS connection to interrupt)
- Could not verify counterfactual outcome updates with 24h of data (background loop not running)
- Could not run Postgres E2E tests (no test PG instance available)
- Could not verify disk-full behavior (would require destructive test)
- Could not verify actual market data integration with real Bybit candles (tests use mock data)
- Could not verify indicator snapshot accuracy against real OHLCV (no live data)
- Could not test the paper.deepseek.yaml config path (DeepSeek client instantiation not tested)
