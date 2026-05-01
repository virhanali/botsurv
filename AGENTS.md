# BotSurv AI Agent Prompt — Maintenance & Improvement Guide

## Context
BotSurv adalah crypto trading bot Go (Golang) yang berjalan di VPS. Mode saat ini: **paper trading** dengan **DeepSeek LLM** sebagai veto agent. Universe scan: **all USDT perpetual** (651 instruments).

## Architecture Overview

```
CLI (cmd/bot)
  ├── config validate
  ├── init (migrations)
  ├── run (main loop)
  ├── positions
  ├── orders
  ├── report
  └── universe refresh

Core Pipeline (per 15m cycle):
  MarketData (Bybit WS) → Universe Scan → Screener → LLM Veto → Risk Engine → Executor → Monitor

Repositories (Postgres):
  candles | universe_symbols | cycles | candidates | positions | orders | executions | account_snapshots | llm_decisions | llm_usage_daily | risk_decisions
```

## Recently Implemented Features (MUST NOT BREAK)

### 1. DeepSeek LLM Provider
- **Request**: `reasoning_effort: "max"`, `thinking: {"type":"enabled"}`
- **No**: `temperature`, `top_p`, `presence_penalty`, `frequency_penalty`
- **Parse**: Only `choices[0].message.content` (NOT `reasoning_content`)
- **RawResponse**: Stores final content JSON, NOT chain-of-thought
- **Fallback**: BLOCK with `ValidationStatus` + `ReasonCodes` on any error
- **Budget**: `MaxCostUSDPerDay` check before API call
- **API Key**: Fail-fast validation in config (`DEEPSEEK_API_KEY` env var)

### 2. LLM Call Caps
Applied in scheduler BEFORE calling LLM:
- `hard_cap_candidates_per_cycle`: Max candidates to evaluate per cycle
- `max_calls_per_cycle`: Max LLM API calls per cycle
- `max_calls_per_day`: Max LLM API calls per day (in-memory, resets on restart)
- **Effective cap**: Strictest positive of all three
- **Sort**: By `candidate_score` descending before truncation
- **Skip reasons**: `LLM_HARD_CAP`, `LLM_CALL_CAP_PER_CYCLE`, `LLM_CALL_CAP_PER_DAY`
- **0 means unlimited**

### 3. Paper Trading End-to-End
- PaperBroker with DB persistence hooks
- Protective orders: SL (STOP_MARKET) + TP (TAKE_PROFIT_MARKET) auto-placed on fill
- Position rehydration from DB on restart
- Kill switch: Daily loss limit triggers emergency close + halt
- Account snapshots persisted per cycle

### 4. Universe Scan
- Mode: `all_usdt_perpetual` (NOT explicit list)
- Layer 1: Scan all 651 USDT perps from Bybit REST
- Layer 2: Quality filter (spread, slippage, depth, ATR)
- Layer 3: Setup engine (breakout detection)
- **No hardcoded coin lists** — any coin can produce candidates

### 5. Safety Guards
- `GetOrderBookSummary` NEVER called with `targetNotional <= 0`
- `target_notional > 0` validated in config
- `orders.order_type` is `VARCHAR(32)` (was 16, too small for TAKE_PROFIT_MARKET)
- Config validation: fail-fast on missing API keys

## Rules for AI Agent

### DO
- ✅ Make minimal changes
- ✅ Add tests for new logic
- ✅ Follow existing code patterns
- ✅ Use structured logging (JSON)
- ✅ Handle errors explicitly, never panic
- ✅ Respect `context.Context` cancellation
- ✅ Run `go test -count=1 ./...` and `go vet ./...` after changes

### DON'T
- ❌ Remove or modify `migrations/` files that already exist
- ❌ Change DeepSeek request/response handling without deep understanding
- ❌ Remove safety guards (targetNotional checks, kill switch)
- ❌ Break backward compatibility in repository interfaces
- ❌ Hardcode coin symbols anywhere
- ❌ Commit secrets or API keys
- ❌ Deploy without user explicit approval

## What to Clean Up / Remove

When reviewing code, look for:

1. **Unused checks in monitor.sh**:
   - `bad_execs` (order_id=0) — removed, not meaningful
   - Any hardcoded config paths

2. **Dead code**:
   - Old OpenRouter-only structs/functions if generalized
   - Unused mock methods in tests
   - Hardcoded `BTCUSDT` references in config (should use `all_usdt_perpetual`)

3. **Schema drift**:
   - Check if all migrations apply cleanly on fresh DB
   - Verify `orders.order_type` width supports all enum values

4. **Config inconsistencies**:
   - `paper.vps.yaml` vs `paper.deepseek.yaml` — keep in sync where applicable
   - Ensure VPS systemd unit points to correct config

## What to Improve

1. **Observability**:
   - Add Prometheus metrics (cycle duration, LLM latency, PnL)
   - Add structured health endpoint

2. **Performance**:
   - Universe scan caching (don't rescan all 651 every cycle)
   - Candle cache warming optimization

3. **Resilience**:
   - Circuit breaker for DeepSeek API (current: 3 retries with backoff)
   - Graceful degradation if LLM timeout

4. **Testing**:
   - More E2E scenarios: LLM_BLOCK, RISK_REJECTED, kill switch trigger
   - Postgres integration tests for all repositories

5. **Security**:
   - Audit log for all trades
   - Rate limiting on CLI commands

## Verification Commands

```bash
# After any code change:
go test -count=1 ./...
go vet ./...
git diff --check

# Postgres E2E (requires local Postgres):
env BOTSURV_TEST_POSTGRES_DSN='postgres://botsurv:botsurv@localhost:5432/botsurv?sslmode=disable' \
  go test -v -count=1 ./internal/scheduler -run 'TestEndToEnd'

# Config validation:
go run ./cmd/bot config validate --config configs/paper.deepseek.yaml

# Live LLM smoke test (costs API money):
source .env && BOTSURV_TEST_LIVE_LLM=1 go test -v -count=1 ./internal/llm -run TestLiveDeepSeek_Smoke -timeout 120s
```

## VPS Quick Reference

- **Host**: `178.105.27.231`
- **User**: `root` (or `botsurv` for service)
- **App dir**: `/opt/botsurv/app`
- **Releases**: `/opt/botsurv/releases`
- **Env**: `/etc/botsurv/paper.env`
- **Service**: `botsurv-paper`
- **DB**: PostgreSQL, database `botsurv`
- **Monitor**: `/opt/botsurv/monitor.sh`
- **Logs**: `journalctl -u botsurv-paper -f`

## Current Config (paper.deepseek.yaml)

- **Mode**: paper
- **Universe**: all_usdt_perpetual
- **LLM**: DeepSeek, deepseek-v4-pro, thinking mode
- **Margin per trade**: $10
- **Max leverage**: 5x
- **Max open positions**: 3
- **LLM caps**: hard_cap=1, max_calls_per_cycle=1, max_calls_per_day=10
- **Max daily loss**: 3%

---

**When asked to make changes:**
1. Read relevant files first
2. Identify what feature/area is affected
3. Make minimal, safe changes
4. Add/update tests
5. Run verification commands
6. Summarize what changed and why
