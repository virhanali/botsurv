# BotSurv v2 Inspection Report

## Repo layout overview

```text
botsurv/
├── cmd/
│   └── bot/
│       └── main.go
├── configs/
│   ├── paper.yaml
│   ├── paper.vps.yaml
│   └── paper.deepseek.yaml
├── deploy/
│   ├── scripts/
│   │   ├── bootstrap-vps.sh
│   │   └── monitor.sh
│   └── systemd/
│       └── botsurv-paper.service
├── docs/
│   ├── architecture.md
│   ├── product-spec.md
│   ├── decisions.md
│   └── phases.md
├── internal/
│   ├── alert/
│   ├── app/
│   ├── broker/
│   ├── cli/
│   ├── db/
│   ├── domain/
│   ├── executor/
│   ├── llm/
│   ├── logger/
│   ├── marketdata/
│   ├── monitor/
│   ├── risk/
│   ├── scheduler/
│   ├── screener/
│   ├── strategy/
│   └── universe/
└── migrations/
    ├── 001_initial.sql
    ├── 002_trades.sql
    ├── 003_llm_decisions.sql
    ├── 004_protective_orders.sql
    ├── 005_widen_order_type.sql
    └── 006_audit_recovery_state.sql
```

Top-level package notes:
- `cmd`: CLI entrypoint wiring.
- `configs`: runtime YAML configs (paper-focused).
- `deploy`: VPS bootstrap and systemd templates.
- `docs`: planning/spec docs (contains forward-looking design, not always current code reality).
- `internal`: all runtime core modules.
- `migrations`: PostgreSQL schema evolution.

## Component inventory

### Market data fetching (Bybit websocket, REST, candle handling)
1. Where it lives:
- `internal/marketdata/bybit_ws.go`
- `internal/marketdata/backfill.go`
- `internal/marketdata/parser.go`
- `internal/marketdata/cache.go`
- `internal/marketdata/orderbook.go`
- `internal/marketdata/trade_flow.go`
- `internal/marketdata/service.go`
2. What it currently does:
- Uses Bybit public WS (`kline`, `tickers`, `orderbook`, `publicTrade`) with reconnect/backoff and startup REST backfill.
- Maintains in-memory caches for candles, latest price, orderbook summary, and rolling trade flow.
- Persists confirmed candles to DB, backfills historical candles via REST and marks them confirmed.
3. What it does NOT do (gaps):
- No alternate market data provider implementation despite `market_data.provider` config (runtime always wires Bybit WS in `internal/cli/run.go`).
- No explicit websocket sequence-gap recovery path besides rejecting pre-snapshot deltas and waiting for next snapshot.
- No BTC dominance (`BTCD`) source integration.
4. Code quality: **acceptable**
5. Test coverage: **yes** (`marketdata_test.go`, `parser_test.go`, `cache_test.go`; some tests require loopback networking)

### Closed-candle handling (distinguish closed vs in-progress candles)
1. Where it lives:
- `internal/domain/domain.go` (`Candle.Confirmed`)
- `internal/marketdata/parser.go` (parses `confirm`)
- `internal/marketdata/bybit_ws.go` (persists only confirmed candles)
- `internal/marketdata/backfill.go` (forces backfill candles to confirmed)
- `internal/screener/screener.go` and `internal/strategy/setup.go` (consume candles)
2. What it currently does:
- The system tracks candle confirmation state and stores confirmed-only candles in DB.
- In-memory cache can hold both unconfirmed and confirmed versions of same open time.
3. What it does NOT do (gaps):
- Setup/regime logic does not filter to confirmed candles before signal generation; latest in-progress candle can be used for setup decisions.
- No hard guard that entry decisions only happen on closed setup candle.
4. Code quality: **acceptable**
5. Test coverage: **partial** (marketdata has confirmation tests; strategy/screener do not enforce confirmed-only path)

### Indicator calculations (which indicators exist, where computed)
1. Where it lives:
- `internal/strategy/setup.go`
- `internal/screener/screener.go`
- `internal/universe/scanner.go` (`computeATR` for Layer 2)
2. What it currently does:
- Implements ATR, EMA, SMA volume, range high/low, body ratio, breakout extension, regime detection, and breakout setup checks.
- Screener computes ATR/EMA and uses setup outputs to create candidates.
3. What it does NOT do (gaps):
- No BTCD/BTC-relative indicators.
- No multi-symbol context indicators (only per-symbol data).
- `MaxDistanceFromBreakoutATR` config exists but is not actually used in setup checks.
4. Code quality: **clean**
5. Test coverage: **yes** (`internal/strategy/setup_test.go`, plus screener tests)

### Market regime / BTC context (BTCUSDT and BTCD awareness)
1. Where it lives:
- Per-symbol regime: `internal/strategy/setup.go` + `internal/screener/screener.go`
- Universe symbol selection: `internal/universe/scanner.go`
2. What it currently does:
- Regime is computed per candidate symbol using its own 1H EMA distance.
- Universe scan includes BTCUSDT only as one normal symbol among USDT perps.
3. What it does NOT do (gaps):
- No dedicated BTCUSDT context feed driving altcoin decisions.
- No BTCD ingestion, storage, or usage.
- No cross-asset gating (e.g., “don’t long alts when BTC regime is X”).
4. Code quality: **acceptable**
5. Test coverage: **partial** (regime logic tested indirectly; no BTC/BTCD context tests because feature is absent)

### Strategy / signal generation (how entries are decided)
1. Where it lives:
- `internal/screener/screener.go` (`evaluateSymbol`)
- `internal/strategy/setup.go` (`DetectSetup`, breakout long/short checks)
2. What it currently does:
- Builds candidates from 1H regime + 15m breakout setup with RR and expected-move checks.
- Produces `MARKET` or `LIMIT_RETEST` entry type and proposed SL/TP.
3. What it does NOT do (gaps):
- No retest lifecycle engine or timeout handling despite `LIMIT_RETEST` output and `limit_retest_ttl_minutes` config.
- No explicit “closed 15m candle only” gate before setup detection.
- Entry type list in config is not used as an allow/deny gate.
4. Code quality: **acceptable**
5. Test coverage: **yes** (`setup_test.go`, `screener_test.go`)

### Scoring (candidate scoring)
1. Where it lives:
- `internal/screener/screener.go` (`computeScoresFromOB`, weighted candidate score)
- `internal/universe/scoring.go` (duplicate scoring/eligibility helpers)
- `internal/universe/scanner.go` (`FilterQuality` uses scoring)
2. What it currently does:
- Computes liquidity/execution/volatility/setup weighted score.
- Applies LLM eligibility checks (`min_candidate_score`, spread/slippage/depth, RR, expected move vs cost).
3. What it does NOT do (gaps):
- Runtime path does not use `universe.FilterQuality`; scoring logic is duplicated between `screener` and `universe`.
- No correlation-aware scoring or BTC-context weighting.
4. Code quality: **acceptable**
5. Test coverage: **partial** (screener and universe scanner tests exist; duplicated logic increases drift risk)

### Risk engine (position sizing, SL/TP validation, daily limits)
1. Where it lives:
- `internal/risk/engine.go`
- Invoked by `internal/scheduler/scheduler.go`
2. What it currently does:
- Enforces deterministic checks: stale data, spread/slippage/depth, daily loss, open position caps, duplicate symbol, SL/TP side validity, RR, expected move vs cost, leverage, min notional.
- Calculates final notional using margin and risk limits, supports `REDUCE_SIZE` multiplier.
3. What it does NOT do (gaps):
- Config fields `max_correlated_alt_positions` and `max_per_symbol_position` are validated but not enforced in `Validate`.
- LLM decision `ALLOW_LIMIT_RETEST` vs `ALLOW_MARKET` is not used as a hard risk/execution switch.
- No explicit BTC-regime-aware hard blocks.
4. Code quality: **clean**
5. Test coverage: **yes** (`internal/risk/engine_test.go`)

### Execution layer (paper, live, both?)
1. Where it lives:
- `internal/executor/executor.go`
- `internal/broker/broker.go`
- `internal/broker/paper.go`
- Wiring: `internal/cli/run.go`
2. What it currently does:
- Executes approved trades through broker interface; quantity derived from risk notional.
- Supports market and limit order requests; paper broker handles fills, SL/TP protective orders, emergency close, rehydration.
3. What it does NOT do (gaps):
- Live broker is not implemented/wired; config validation hard-rejects live mode.
- No separate shadow execution path.
4. Code quality: **clean**
5. Test coverage: **yes** (`executor_test.go`, `paper_test.go`)

### Shadow mode (does it exist?)
1. Where it lives:
- No dedicated implementation found.
2. What it currently does:
- None.
3. What it does NOT do (gaps):
- No “signal-only/ghost execution” mode that records hypothetical trades without affecting paper broker state.
- No side-by-side live-feed validation channel.
4. Code quality: **none (feature missing)**
5. Test coverage: **none**

### Logging and database (what gets logged, where stored)
1. Where it lives:
- Logging: `internal/logger/logger.go`
- Persistence interfaces/impl: `internal/db/repositories.go`, `internal/db/postgres_impl.go`
- Schema: `migrations/*.sql`
- Cycle persistence hooks: `internal/scheduler/scheduler.go`
2. What it currently does:
- Structured JSON logs to stdout/stderr with level filtering.
- Stores candles, universe symbols, cycles, candidates, positions, orders, executions, account snapshots, LLM decisions, risk decisions, daily LLM usage.
- Paper broker persists and rehydrates core state.
3. What it does NOT do (gaps):
- No dedicated audit trail table for “hard-block reason snapshots per cycle step” beyond reason codes in existing tables.
- No metrics/telemetry subsystem (Prometheus etc.) in runtime code.
4. Code quality: **clean**
5. Test coverage: **partial** (logger unit tests present; DB has strong integration tests but requires reachable Postgres)

### LLM integration (current role, prompt, call path)
1. Where it lives:
- `internal/llm/client.go`
- `internal/scheduler/scheduler.go`
- `internal/screener/screener.go` (context builder)
2. What it currently does:
- LLM is veto-stage between screener and risk; returns `ALLOW_MARKET`, `ALLOW_LIMIT_RETEST`, `REDUCE_SIZE`, `BLOCK`.
- System prompt enforces strict JSON output and veto semantics.
- Scheduler applies per-cycle/day call caps and persists LLM decisions.
3. What it does NOT do (gaps):
- No model output schema versioning or prompt-template registry.
- `llm_routing.max_cost_usd_per_day` is not directly enforced in scheduler; only call-count caps are scheduler-enforced, while token-cost budget is tracked in client (`llm.budget.max_cost_usd_per_day`).
- LLM decision type is not used to switch execution mode (decision mostly acts as block/size reduction gate).
4. Code quality: **acceptable**
5. Test coverage: **yes** (`internal/llm/client_test.go`; live smoke optional via env)

### Config management (env vars, YAML, hardcoded values)
1. Where it lives:
- `internal/app/config.go`
- `internal/cli/root.go` / `internal/cli/config.go`
- `configs/*.yaml`
2. What it currently does:
- YAML config with strict field decoding (`KnownFields(true)`), env substitution `${VAR}`, and deep validation.
- Loads `.env` automatically on CLI startup.
- Strong fail-fast guards for unsupported live mode and missing API keys when LLM enabled.
3. What it does NOT do (gaps):
- Several validated config fields are currently unused in runtime behavior (`max_cycle_overlap`, `entry_types`, `limit_retest_ttl_minutes`, `max_correlated_alt_positions`, `max_per_symbol_position`).
- Hardcoded fee/slippage estimates still exist in screener context/scoring path instead of consistently using config/broker values.
4. Code quality: **clean**
5. Test coverage: **yes** (`internal/app/config_test.go`)

### Tests (which modules have tests, which don’t)
1. Where it lives:
- Tests present in: `internal/app`, `broker`, `db`, `executor`, `llm`, `logger`, `marketdata`, `monitor`, `risk`, `scheduler`, `screener`, `strategy`, `universe`.
- No tests in: `internal/alert`, `internal/cli`, `internal/domain`.
2. What it currently does:
- Broad module-level coverage exists and includes integration-style tests for DB and end-to-end scheduler flow.
3. What it does NOT do (gaps):
- Some tests depend on environment capabilities (Postgres, loopback listeners), so portability is limited in restricted runtimes.
4. Code quality: **acceptable**
5. Test coverage: **partial overall**
- Static coverage breadth is good.
- Runtime verification in this environment is constrained: `go test ./...` failed in `internal/db`, `internal/llm`, `internal/marketdata`, `internal/scheduler`, `internal/universe` due sandbox/network restrictions (`connect: operation not permitted` / `httptest listen ... operation not permitted`), not from confirmed compile errors in those packages.

## Gap analysis

### Already implemented (reusable as-is)
- Bybit WS + REST backfill data ingestion with cache + staleness checks.
- Candidate generation pipeline (universe refresh -> screener -> LLM -> risk -> executor).
- Deterministic risk engine with broad guardrails and reason codes.
- Paper broker with protective SL/TP invariants, rehydration, and emergency close.
- PostgreSQL persistence for operational entities (cycles/candidates/orders/positions/executions/LLM/risk decisions).

### Partially implemented (needs extension)
- Closed-candle model exists (`Confirmed`) but signal path does not enforce confirmed-only setup candles.
- LLM routing and budgeting split across scheduler/client; cost cap enforcement is not unified.
- Candidate scoring/eligibility exists in two places (`screener` and `universe`) with duplication risk.
- Limit-retest concept exists, but lifecycle/TTL semantics are not fully implemented.
- Portfolio controls include fields for correlation/per-symbol caps, but enforcement is incomplete.

### Missing entirely
- Shadow mode (non-executing validation track).
- Live broker execution path.
- BTC-context/BTCD-aware regime or gating logic.
- Correlation engine for alt exposure/risk clustering.

### Risky / conflicting (existing code that may fight v2.1 design)
- `market_data.provider` is config-driven but runtime wiring is effectively hardcoded to Bybit WS.
- Setup path can use in-progress candles; this conflicts with strict closed-candle TA-first behavior.
- Runtime ignores several risk/strategy config fields, creating false confidence in configured safeguards.
- Duplicate scoring logic across modules can diverge under refactor.
- `universe.FilterQuality` contains placeholder setup score logic and is not part of current execution path (potential dead/legacy path).

## Refactor risk map

- `internal/app/config.go`: **SAFE_TO_EXTEND**
- `internal/marketdata/bybit_ws.go`: **WRAP**
- `internal/marketdata/backfill.go`: **LEAVE_ALONE**
- `internal/marketdata/cache.go`: **LEAVE_ALONE**
- `internal/universe/scanner.go`: **WRAP**
- `internal/universe/scoring.go`: **REPLACE** (duplicate logic + partly unused runtime path)
- `internal/strategy/setup.go`: **WRAP**
- `internal/screener/screener.go`: **WRAP**
- `internal/llm/client.go`: **WRAP**
- `internal/risk/engine.go`: **WRAP**
- `internal/executor/executor.go`: **SAFE_TO_EXTEND**
- `internal/broker/paper.go`: **LEAVE_ALONE**
- `internal/scheduler/scheduler.go`: **WRAP**
- `internal/db/postgres_impl.go`: **LEAVE_ALONE**
- `migrations/*.sql`: **LEAVE_ALONE**

## Recommended Phase 1 entry points

Safest initial files for data validation + hard blocks:
1. `internal/screener/screener.go` (enforce confirmed-candle-only candidate generation and stronger pre-LLM hard blocks)
2. `internal/risk/engine.go` (add missing hard blocks: per-symbol and correlated exposure enforcement)
3. `internal/scheduler/scheduler.go` (centralize cap enforcement and reject-path observability)
4. `internal/marketdata/bybit_ws.go` (explicitly tag/route in-progress vs closed candle consumption)
5. `internal/app/config.go` (align validation with actual runtime-enforced features/flags)

## Open questions for the team

- Should setup/regime calculations use only confirmed 15m/1H candles, with strict cycle timing tied to candle close?
- For v2.1 BTC/BTCD awareness: what is the authoritative BTCD data source and update cadence?
- Should LLM decision `ALLOW_LIMIT_RETEST` be a strict execution directive, or remain advisory only?
- Are `max_correlated_alt_positions` and `max_per_symbol_position` intended as hard reject rules now, or future-only?
- Should `market_data.provider` continue to advertise multiple providers before non-Bybit implementations exist?
- Is `universe.FilterQuality` intended to be reintroduced in runtime flow or retired to avoid duplicate scoring paths?
