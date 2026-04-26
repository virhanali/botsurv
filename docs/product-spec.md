# Agentic AI Crypto Futures Trading Bot - Product Spec

You are a senior Golang backend engineer, trading system architect, quant trading engineer, and production reliability engineer.

I want to build a production-grade Agentic AI Crypto Futures Trading Bot.

This bot runs as a terminal app on a VPS or local machine.

## Important Reality

- Do not claim guaranteed profit.
- The goal is to build a robust production-grade trading system that supports paper mode and later live mode.
- Profitability must be validated by paper trading, backtesting, and live small-size data.
- Engineering quality, logging, risk control, and operational safety are as important as the strategy.

## Core Principles

- Production-grade architecture.
- WebSocket-first market data.
- REST only for bootstrap, backfill, recovery, and reconciliation.
- Auto Mode trading.
- Single user only. No multi-tenant user management.
- Config file is enough for settings.
- Paper/live switch is config-driven.
- Strategy, LLM, Risk Engine, Executor, MarketData, Broker, Logger, and Monitor must be modular.
- LLM is NOT the trader.
- LLM is only a veto/context agent.
- LLM cannot create trades freely.
- LLM cannot change trade side.
- LLM cannot set arbitrary entry, SL, TP, or size.
- Setup Engine calculates proposed trade deterministically.
- Risk Engine is the final authority.
- If anything is invalid, stale, unsafe, inconsistent, or uncertain, skip trade and log the reason.
- Every cycle must be logged, including skipped trades.
- Never leave a futures position without protective stop.
- Scan broad universe, but use minimal indicators to avoid noise.
- Aggressive trading is allowed only inside hard risk boundaries.

## Tech Stack

- Language: Golang
- Runtime: terminal app on VPS/local
- Config: YAML
- Database: PostgreSQL only
- Repository layer must target PostgreSQL
- Logs: structured JSON logs
- Metrics: Prometheus-compatible if practical
- Alerts: Telegram bot or generic webhook notification
- LLM Provider: OpenRouter using OpenAI-compatible Chat Completions API
- Market data: WebSocket-first
- Broker mode:
  - paper
  - live later

## Mode Switching

Support config-driven modes.

### Paper live simulation

```yaml
app:
  mode: paper

market_data:
  provider: bybit_ws

broker:
  provider: paper
```

### Offline/backtest

```yaml
app:
  mode: paper

market_data:
  provider: postgres

broker:
  provider: paper
```

### Future live

```yaml
app:
  mode: live

market_data:
  provider: bybit_ws

broker:
  provider: bybit_live
```

Rules:

- Strategy Engine must not know whether broker is paper or live.
- Risk Engine must not know whether broker is paper or live.
- LLM Veto Agent must not know whether broker is paper or live.
- Only Broker adapter changes.

## Trading Timeframe

- 1H = regime/context
- 15m = setup and entry
- optional 5m = execution context only

## Less-Is-More Strategy Policy

The bot may scan many symbols, but the strategy must use a minimal indicator set.

Allowed core indicators/metrics:

1. ATR
   - volatility health
   - stop buffer
   - breakout extension check
   - price drift validation

2. EMA200 on 1H
   - regime/bias only
   - not an entry trigger

3. 15m range high/low
   - last 20 candles
   - breakout/retest setup

4. Volume ratio
   - current volume vs SMA20 volume
   - breakout confirmation only

5. Execution metrics
   - spread
   - order book depth
   - estimated slippage
   - depth_to_position_size_ratio

Optional but disabled by default:

- order flow rolling buy/sell ratio

Do not implement these as entry indicators in the initial strategy:

- RSI
- MACD
- stochastic
- Ichimoku
- EMA crossover
- Bollinger Band as signal
- formal candlestick pattern recognition
- Fibonacci auto-trading
- complex sentiment/news-based entry

Design goal:

- Scan broad universe.
- Trade only simple, high-quality setups.
- Avoid indicator stacking and overfitting.
- Every rule must have a clear purpose:
  - liquidity
  - volatility
  - regime
  - setup
  - execution
  - risk

If a new indicator is added later, it must be behind a feature flag and evaluated with A/B paper trading before enabling.

## Universe Scanning Policy

The bot may scan broad/all USDT perpetual universe, but must not fully analyze all pairs with Strategy + LLM.

Use funnel-based universe scanner.

### Layer 1 - All Pairs Light Scanner

- Fetch all available USDT perpetual symbols from public market metadata.
- Filter:
  - status = trading
  - quote = USDT
  - not delisted
  - not blacklisted
  - 24h volume above threshold
  - spread below threshold if available
- Output symbols by liquidity/activity.

### Layer 2 - Market Quality Filter

For filtered symbols, check:

- order book depth
- estimated slippage
- ATR health
- abnormal volatility
- funding window if available

Output quality symbols.

### Layer 3 - Setup Scanner

For quality symbols, run deterministic setup engine:

- 1H regime
- 15m compression/range
- breakout
- retest
- volume confirmation
- RR potential

Output valid ProposedTrade candidates.

### Layer 4 - Dynamic GPT Veto + Risk

- Do not use a hard fixed max_candidates_per_cycle as the only gate.
- The bot must support dynamic LLM routing.
- Send every high-quality candidate to LLM if it passes objective quality thresholds.
- LLM must never receive raw all-pair market data.
- LLM receives only compact context for filtered candidates.
- Risk Engine remains final authority.
- Even if LLM allows many trades, Risk Engine must enforce portfolio-level constraints.

Candidate is eligible for LLM if:

- market data is healthy
- liquidity filter passed
- spread filter passed
- depth filter passed
- slippage estimate passed
- ATR health passed
- Setup Engine produced valid ProposedTrade
- RR >= configured min_rr
- expected move > 3x total cost
- candidate_score >= llm_routing.min_candidate_score

## Dynamic LLM Routing

Config example:

```yaml
llm_routing:
  mode: dynamic
  min_candidate_score: 75
  max_calls_per_cycle: 0
  max_calls_per_day: 0
  max_cost_usd_per_day: 3.0
  require_execution_ok: true
  require_liquidity_ok: true
  hard_cap_candidates_per_cycle: 0
```

Rules:

- 0 means unlimited by count, controlled by quality + budget.
- If 0 candidates pass, send 0 to LLM.
- If 10 candidates pass, LLM may evaluate 10 candidates.
- Never send raw all-pair market data to LLM.
- Only send compact context per candidate.
- If budget cap is reached:
  - fallback decision = BLOCK
  - log LLM_BUDGET_EXCEEDED
- LLM routing must be observable:
  - log candidate_score
  - log why candidate was sent to LLM
  - log why candidate was not sent to LLM

## Portfolio Risk Control

Risk Engine must enforce portfolio-level constraints.

Config example:

```yaml
portfolio_risk:
  max_open_positions: 3
  max_new_positions_per_cycle: 2
  max_total_exposure_usd: 300
  max_total_margin_used_pct: 50
  max_risk_per_trade_pct: 0.5
  max_daily_loss_pct: 3
  max_same_direction_positions: 2
  max_correlated_alt_positions: 2
  max_per_symbol_position: 1
```

Rules:

- If many LLM-approved candidates exist, rank them by final_score.
- Execute only the best candidates within portfolio constraints.
- Skip the rest with reason PORTFOLIO_RISK_LIMIT.
- Never exceed max open positions.
- Never exceed max new positions per cycle.
- Never exceed max total exposure.
- Never exceed max daily loss.
- Avoid opening too many same-direction alt positions.
- Do not open duplicate position on same symbol unless explicitly enabled.

## OpenRouter

Use OpenRouter as default LLM provider.

Requirements:

- Base URL configurable, default https://openrouter.ai/api/v1
- API key env: OPENROUTER_API_KEY
- Model configurable
- Optional headers:
  - HTTP-Referer from OPENROUTER_SITE_URL
  - X-Title from OPENROUTER_APP_NAME
- Temperature 0
- Timeout configurable
- Save raw response before parsing
- If API fails, timeout, rate-limits, returns empty/malformed output, or invalid schema: fallback BLOCK
- LLM failure must not stop position monitoring
- LLM failure only skips new trade

## LLM Veto Agent

LLM is only allowed to evaluate proposed trade.

LLM cannot:

- create new trade
- change side
- set entry
- set SL
- set TP
- set arbitrary size
- override Risk Engine

Allowed decisions:

- ALLOW_MARKET
- ALLOW_LIMIT_RETEST
- REDUCE_SIZE
- BLOCK

Allowed size_multiplier:

- 1.0
- 0.75
- 0.5
- 0.25
- 0.0

Output schema:

```json
{
  "decision": "ALLOW_MARKET | ALLOW_LIMIT_RETEST | REDUCE_SIZE | BLOCK",
  "confidence": 0.0,
  "size_multiplier": 1.0,
  "regime": "trend_up | trend_down | range | chop",
  "reason_codes": [],
  "risk_flags": [],
  "notes": "max 2 sentences"
}
```

Validation:

- invalid JSON => BLOCK
- invalid decision enum => BLOCK
- invalid size_multiplier => BLOCK
- confidence < 0.6 => BLOCK
- raw response must be stored

## Sizing

```text
position_by_margin = margin_per_trade_usd * max_leverage

risk_amount = equity_usd * (max_risk_per_trade_pct / 100)

position_by_risk = risk_amount / stop_loss_percent

base_position_notional = min(position_by_margin, position_by_risk)

if LLM decision == REDUCE_SIZE:
    final_position_notional = base_position_notional * size_multiplier
else:
    final_position_notional = base_position_notional
```

## Risk Engine Final Authority

Validate:

- LLM decision valid
- signal freshness valid
- market data not stale
- price drift acceptable
- spread acceptable
- depth acceptable
- slippage estimate acceptable
- daily loss not breached
- max open positions not breached
- max new positions per cycle not breached
- max total exposure not breached
- max total margin used not breached
- max same direction positions not breached
- max correlated alt positions not breached
- max per symbol position not breached
- not in cooldown
- no orphan position
- no conflicting open orders
- SL and TP correct side
- RR >= configured min_rr, default 1:2
- expected move > 3x total cost
- min notional satisfied
- leverage valid
- liquidation price not too close
- broker state consistent

If any validation fails:

- skip trade
- log reason
- alert if dangerous

## Paper Broker

PaperBroker must simulate futures trading realistically:

- starting balance
- available balance
- used margin
- equity
- realized PnL
- unrealized PnL
- fees
- slippage
- open positions
- open orders
- closed positions

Supported order types:

- MARKET
- LIMIT
- STOP_MARKET
- TAKE_PROFIT_MARKET

Paper fill behavior:

- Market order fills at latest price adjusted by configured slippage
- Limit order fills if candle high/low touches limit price
- SL triggers if candle crosses stop price
- TP triggers if candle crosses take profit price
- If both SL and TP touched inside same candle, use conservative assumption by default
- Entry and exit fees applied
- Position must never exist without SL

## Market Data Service

Implement WebSocket-first MarketDataService.

Primary provider:

- BybitWSMarketDataService

Fallback/future providers:

- PostgreSQLHistoricalMarketDataService
- MockMarketDataService
- OKXWSMarketDataService
- BitgetWSMarketDataService

BybitWSMarketDataService requirements:

- Use public WebSocket, no auth required
- Subscribe to:
  - kline 15m
  - kline 1H
  - ticker/latest price
  - orderbook depth 50
  - public trades
- On startup, use REST backfill:
  - 15m last 500 candles
  - 1H last 500 candles
- Use WebSocket for realtime updates
- Persist closed candles to DB
- Maintain in-memory latest candle state
- Maintain latest price
- Maintain local orderbook from snapshot/delta
- Maintain trade flow rolling windows:
  - 15 seconds
  - 60 seconds
  - 300 seconds
- Detect stale data
- Auto reconnect
- Resubscribe after reconnect
- Backfill missing candles after reconnect
- Expose health status per symbol
- Expose last update timestamp per symbol

Important:

- WebSocket events update MarketDataService state.
- Trading cycle reads from MarketDataService/cache/DB.
- WebSocket events must not directly execute trades.
- If market data is stale, Risk Engine must reject trade.

## External Signal Source

The bot must support multiple signal sources.

Signal sources:

- InternalStrategySignalSource
- ManualSignalSource
- WebhookSignalSource optional later
- TelegramSignalSource optional later only if compliant/safe

Initial production must implement:

- InternalStrategySignalSource
- ManualSignalSource

ManualSignalSource:

- User can input external signal from CLI
- Source name example: cryptocium
- Inputs:
  - symbol
  - side LONG/SHORT
  - entry_min
  - entry_max
  - stop_loss
  - tp1
  - tp2
  - signal_time optional
  - notes optional
- Bot converts manual signal into ProposedTrade
- Risk Engine validates it
- LLM may evaluate it
- PaperBroker executes only if approved
- External signal added to external_signal_watchlist
- External signal cannot bypass Risk Engine

## CLI Commands

- bot init --config configs/prod.yaml
- bot preflight --config configs/prod.yaml
- bot run --config configs/prod.yaml
- bot run-once --config configs/prod.yaml
- bot positions --config configs/prod.yaml
- bot orders --config configs/prod.yaml
- bot report daily --config configs/prod.yaml
- bot emergency-close-all --config configs/prod.yaml
- bot backtest --config configs/prod.yaml --from YYYY-MM-DD --to YYYY-MM-DD
- bot config validate --config configs/prod.yaml
- bot state --config configs/prod.yaml
- bot universe list
- bot universe refresh
- bot universe add SYMBOL --force
- bot universe remove SYMBOL
- bot signal add --source SOURCE --symbol SYMBOL --side LONG|SHORT --entry-min X --entry-max Y --sl Z --tp1 A --tp2 B
- bot signal list --source SOURCE
- bot signal stats --source SOURCE

## Deployment

Provide:

- Dockerfile
- docker-compose.yml for app + PostgreSQL
- systemd service example
- .env.example
- configs/paper.yaml
- configs/live.example.yaml
- README local run
- README VPS run
- backup script
- restore script
- migration command
- graceful shutdown

## Safety Invariants

A trade may only execute if all are true:

1. Market data is healthy.
2. Setup Engine produced valid proposed trade.
3. LLM decision is not BLOCK.
4. Risk Engine approved.
5. Signal is fresh.
6. Spread/depth/slippage are valid.
7. Position size is deterministic.
8. Daily loss is not breached.
9. Portfolio risk limits are not breached.
10. SL/TP are valid.
11. Broker state is consistent.
12. Protective stop can be created.

## Do Not

- Do not claim guaranteed profit.
- Do not let LLM directly place trades.
- Do not let LLM set arbitrary size.
- Do not bypass Risk Engine.
- Do not trade with stale market data.
- Do not skip logging skipped trades.
- Do not auto-mutate strategy from LLM review.
- Do not leave position without SL.
- Do not enable live mode without explicit config and env guard.
- Do not implement everything in one huge file.
- Do not add unnecessary indicators.

## Before Coding Any Phase

1. Inspect current codebase.
2. Summarize what exists.
3. Identify gaps/safety issues.
4. List files to modify/create.
5. List tests to add.
6. Implement only the requested phase.
