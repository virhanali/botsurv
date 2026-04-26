# Implementation Phases

Rules:

- Implement one phase at a time.
- Do not implement future phases unless explicitly requested.
- Every phase must include tests for safety-critical logic.
- After each phase, run code review before moving on.
- Strategy must stay less-is-more.
- LLM must remain veto-only.
- Risk Engine remains final authority.

---

## PHASE 0 - Architecture & Planning

Do not write production code yet.

Read:

- docs/product-spec.md

Produce:

1. Architecture summary
2. Component boundaries
3. Folder structure
4. Database schema draft
5. Config YAML draft
6. Domain model draft
7. MarketDataService design
8. PaperBroker design
9. Universe scanner design
10. Dynamic LLM routing design
11. Portfolio risk control design
12. Less-is-more strategy design
13. External signal source design
14. Risk Engine design
15. LLM Veto Agent design
16. Failure mode table
17. Implementation roadmap from Phase 1 to Phase N

Important:

- WebSocket-first market data.
- REST only for bootstrap/backfill/recovery.
- PaperBroker first, but live-ready architecture.
- Single-user config-based system.
- Broad universe scanning, but minimal indicators.
- Dynamic LLM routing based on candidate_score.
- LLM is veto/context only.
- Risk Engine final authority.

After producing the plan, wait for approval.

---

## PHASE 1 - Foundation

Implement:

- Go module setup
- Folder structure
- CLI skeleton
- YAML config loader
- Config validation
- .env loading
- DB connection layer
- Repository interfaces
- PostgreSQL support
- Migration runner
- Structured JSON logger

Core domain models:

- Candle
- SymbolInfo
- UniverseSymbol
- Cycle
- Candidate
- ProposedTrade
- LLMDecision
- RiskDecision
- Order
- Position
- Execution
- PnLEvent
- AccountState
- BotState
- UserConfig
- MarketDataHealth
- LLMRoutingConfig
- PortfolioRiskConfig

Candidate fields:

- candidate_score
- liquidity_score
- execution_score
- setup_score
- volatility_score
- llm_eligible
- llm_routing_reason_codes

RiskDecision fields:

- approved
- final_position_notional
- required_margin
- estimated_loss
- reason_codes
- portfolio_rank
- portfolio_reject_reason

CLI commands:

- bot init
- bot config validate
- bot state

Do not implement:

- trading logic
- WebSocket
- OpenRouter
- PaperBroker

Tests:

- config loading
- config validation
- LLM routing config validation
- portfolio risk config validation
- PostgreSQL DB migration
- repository basic insert/read
- candidate score field persistence
- logger initialization

---

## PHASE 2 - MarketDataService WebSocket Core

Implement MarketDataService interface:

- Start(ctx)
- Stop(ctx)
- GetCandles(ctx, symbol, timeframe, limit)
- GetLatestPrice(ctx, symbol)
- IsHealthy(symbol)
- LastUpdate(symbol)

Implement BybitWSMarketDataService initial version:

- Public WebSocket connection
- Subscribe kline 15m
- Subscribe kline 1H
- Subscribe ticker/latest price
- Detect candle close/confirm event
- Persist closed candles to DB
- Maintain in-memory candle cache
- Maintain latest price cache
- Auto reconnect
- Resubscribe after reconnect
- Stale data detection
- Graceful shutdown

REST support:

- Backfill 15m last 500 candles on startup
- Backfill 1H last 500 candles on startup
- Store backfilled candles in DB
- Avoid duplicate candle insert

Do not implement:

- orderbook
- trades stream
- strategy

Tests:

- WebSocket message parser
- candle close detection
- candle cache update
- duplicate candle prevention
- stale data detection
- REST backfill parser with mock HTTP
- reconnect/resubscribe behavior with mock

---

## PHASE 3 - Orderbook + Trade Flow

Extend BybitWSMarketDataService.

Implement:

- Subscribe orderbook depth 50
- Parse snapshot
- Parse delta
- Maintain local orderbook
- Rebuild/reset local orderbook on snapshot
- Compute:
  - best bid
  - best ask
  - spread bps
  - bid depth
  - ask depth
  - depth_to_position_size_ratio
  - estimated slippage bps for target notional
- Subscribe public trades
- Maintain rolling trade flow windows:
  - 15s
  - 60s
  - 300s
- Compute:
  - aggressive buy volume
  - aggressive sell volume
  - buy/sell ratio
  - trade count
- Persist summaries if useful

Expose:

- GetOrderBookSummary(ctx, symbol)
- GetTradeFlow(ctx, symbol)

Tests:

- orderbook snapshot parse
- orderbook delta merge
- orderbook reset on snapshot
- spread calculation
- depth calculation
- depth_to_position_size_ratio
- slippage estimate
- trade flow rolling window
- stale orderbook detection

---

## PHASE 4 - Universe Scanner

Implement funnel-based universe scanning.

Layer 1 - All Pairs Light Scanner:

- Fetch all USDT perpetual symbols from public market metadata.
- Filter:
  - status = trading
  - quote = USDT
  - not blacklisted
  - volume 24h above threshold
  - spread below threshold if available
- Output symbols with liquidity/activity score.

Layer 2 - Market Quality Filter:

- order book depth
- estimated slippage
- ATR health
- abnormal volatility
- funding window if available

Config:

```yaml
universe:
  mode: hybrid
  refresh_interval_minutes: 60
  light_scan_max_symbols: 100
  quality_filter_max_symbols: 30
  setup_scan_max_symbols: 10
  core_symbols: []
  force_include_symbols: []
  blacklist: []
  external_signal_watchlist:
    enabled: true
    ttl_hours: 48
```

Rules:

- All pairs can be scanned lightly.
- Only quality symbols get deeper setup analysis.
- Force-included symbols still must pass Risk Engine.
- External signal symbols can be added to watchlist.
- No symbol can bypass Risk Engine.

Candidate scoring:

```text
candidate_score =
  liquidity_score * 0.25
+ execution_score * 0.25
+ setup_score * 0.35
+ volatility_score * 0.15
```

LLM eligibility:

Candidate is LLM eligible if:

- candidate_score >= llm_routing.min_candidate_score
- execution filters passed
- liquidity filters passed
- setup is valid
- RR >= min_rr
- expected move > 3x cost

Do not impose top 3 unless hard_cap_candidates_per_cycle is configured.

CLI:

- bot universe list
- bot universe refresh
- bot universe add SYMBOL --force
- bot universe remove SYMBOL

Tests:

- all pair metadata filtering
- blacklist filtering
- force include behavior
- quality filter
- candidate above threshold becomes llm_eligible
- candidate below threshold not eligible
- 10 good candidates can all become llm_eligible
- hard cap works only if configured
- external watchlist TTL

---

## PHASE 5 - PaperBroker Production

Implement Broker interface:

- GetAccountState
- GetOpenPositions
- GetOpenOrders
- PlaceOrder
- CancelOrder
- ClosePosition
- EmergencyCloseAll

Implement PaperBroker:

- starting balance
- available balance
- used margin
- equity
- realized PnL
- unrealized PnL
- total fees
- total slippage
- open positions
- open orders
- closed positions

Order types:

- MARKET
- LIMIT
- STOP_MARKET
- TAKE_PROFIT_MARKET

Paper fill behavior:

- Market order fills at latest price adjusted by configured slippage
- Limit order fills if candle high/low touches limit price
- SL triggers if candle crosses stop price
- TP triggers if candle crosses TP price
- If both SL and TP touched inside same candle, use conservative SL-first assumption by default
- Entry and exit fees applied
- Slippage applied
- Margin usage tracked
- Realized and unrealized PnL updated
- Position must never exist without SL
- If protective SL/TP creation fails in paper state, emergency close position and log dangerous error

Tests:

- market order fill
- limit order fill
- long SL trigger
- long TP trigger
- short SL trigger
- short TP trigger
- same-candle SL/TP conservative logic
- fee calculation
- slippage calculation
- margin usage
- realized PnL
- unrealized PnL
- emergency close

---

## PHASE 6 - Risk Engine

Implement deterministic Risk Engine.

Input:

- UserConfig
- AccountState
- ProposedTrade
- LLMDecision
- CurrentMarketState
- BotState
- CurrentPortfolioState

Output:

- APPROVED or REJECTED
- final_position_notional
- required_margin
- estimated_loss
- reason_codes
- portfolio_rank
- portfolio_reject_reason

Validations:

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
- duplicate symbol position rejection unless enabled
- not in cooldown
- no conflicting open orders
- SL and TP correct side
- RR >= configured min_rr
- expected move > 3x estimated total cost
- min notional satisfied
- leverage valid
- liquidation price not too close

Sizing:

```text
position_by_margin = margin_per_trade_usd * max_leverage
risk_amount = equity_usd * max_risk_per_trade_pct / 100
position_by_risk = risk_amount / stop_loss_percent
base_position_notional = min(position_by_margin, position_by_risk)

If LLM decision == REDUCE_SIZE:
final_position_notional = base_position_notional * size_multiplier
```

Portfolio-level behavior:

If multiple LLM-approved candidates exist:

- rank by final_score
- approve only candidates within portfolio limits
- reject rest with PORTFOLIO_RISK_LIMIT

Tests:

- long SL/TP validation
- short SL/TP validation
- invalid RR rejection
- sizing by margin
- sizing by risk
- REDUCE_SIZE multiplier
- daily loss rejection
- max open position rejection
- max new positions per cycle rejection
- max total exposure rejection
- max margin used rejection
- duplicate symbol position rejection
- too many same-direction trades rejection
- cooldown rejection
- min notional rejection
- stale data rejection
- invalid LLM rejection
- approve best ranked candidates within limits

---

## PHASE 7 - LLM OpenRouter Veto

Implement:

- LLMClient interface
- MockLLMClient
- OpenRouterClient
- strict output validation
- fallback BLOCK behavior
- raw response persistence
- usage/cost tracking if practical

OpenRouter requirements:

- base_url configurable
- API key from OPENROUTER_API_KEY
- model from config
- optional headers:
  - HTTP-Referer from OPENROUTER_SITE_URL
  - X-Title from OPENROUTER_APP_NAME
- timeout configurable
- temperature 0
- max tokens configurable

Allowed decisions:

- ALLOW_MARKET
- ALLOW_LIMIT_RETEST
- REDUCE_SIZE
- BLOCK

Allowed size multipliers:

- 1.0
- 0.75
- 0.5
- 0.25
- 0.0

Fallback rules:

- API error => BLOCK
- timeout => BLOCK
- rate limit => BLOCK
- invalid JSON => BLOCK
- invalid enum => BLOCK
- invalid multiplier => BLOCK
- confidence < 0.6 => BLOCK
- budget exceeded => BLOCK and log LLM_BUDGET_EXCEEDED

Tests:

- valid ALLOW_MARKET
- valid ALLOW_LIMIT_RETEST
- valid REDUCE_SIZE
- valid BLOCK
- malformed JSON fallback BLOCK
- confidence below threshold fallback BLOCK
- invalid enum fallback BLOCK
- invalid multiplier fallback BLOCK
- API error fallback BLOCK
- budget exceeded fallback BLOCK

---

## PHASE 8 - Strategy / Setup Engine

Implement deterministic setup engine using only allowed minimal metrics.

Allowed core:

- ATR
- EMA200 1H
- 15m range high/low
- volume ratio vs SMA20
- execution metrics from MarketDataService

Do not add:

- RSI
- MACD
- Stochastic
- EMA crossover
- candlestick pattern recognition
- Fibonacci
- Ichimoku

Implement indicators:

- ATR
- EMA200
- SMA volume
- range high/low last 20 candles
- candle body ratio
- breakout extension in ATR

1H regime:

- trend_up
- trend_down
- range
- chop

15m setup:

- compression/range
- breakout
- retest
- continuation optional but disabled by default

Breakout logic:

- range last 20 candles
- close outside range high/low
- volume > 1.3x SMA20 volume
- breakout candle body ratio healthy
- breakout candle not too extended, default < 2.5x ATR
- distance from breakout level < 1 ATR, otherwise propose LIMIT_RETEST
- expected move > 3x total cost estimate
- RR >= 1:2

ProposedTrade output:

- symbol
- side LONG/SHORT
- setup_type
- regime
- entry_type MARKET/LIMIT_RETEST
- proposed_entry
- proposed_stop_loss
- proposed_take_profit
- stop_loss_pct
- take_profit_pct
- rr
- invalidation_level
- reason_codes
- setup_score

Tests:

- bullish breakout long proposal
- bearish breakout short proposal
- extended breakout => LIMIT_RETEST or HOLD
- volume too low => HOLD
- RR too low => HOLD
- trend mismatch => HOLD
- range calculation
- ATR calculation
- EMA200 regime
- setup_score calculation

---

## PHASE 9 - Screener + Context Builder

Screener:

- Takes universe quality-filtered symbols
- Runs setup engine only on setup_scan symbols
- Ranks candidates by:
  - liquidity_score
  - volatility_score
  - setup_score
  - execution_score
- Outputs all candidates that pass deterministic setup and LLM eligibility threshold
- Mark each candidate with llm_eligible true/false
- Candidates not eligible are logged with reason codes

Dynamic LLM Routing:

- Candidates eligible for LLM are sent to Context Builder
- Do not send all raw pairs to LLM
- Do not impose fixed top 3 unless hard cap is configured

Context Builder:

- Build compact deterministic JSON per candidate
- One candidate = one LLM veto request
- Do not dump raw candles by default

Context includes:

- candidate summary
- 1H regime summary
- 15m setup detail
- optional 5m execution context if available
- order book summary
- trade flow summary if enabled
- cost and economics
- proposed trade
- risk state
- market data health

Tests:

- candidate ranking
- multiple eligible candidates generate multiple LLM contexts
- non-eligible candidates are logged but not sent to LLM
- context contains proposed trade
- context contains risk state
- unavailable data handled
- JSON valid
- context stays compact

---

## PHASE 10 - Executor

Implement Executor using Broker interface.

If RiskDecision approved:

- Place main order:
  - MARKET
  - LIMIT_RETEST
- Wait for fill confirmation
- After fill:
  - create STOP_MARKET SL
  - create TAKE_PROFIT_MARKET TP
  - verify protective orders immediately
  - retry protective order placement up to 3 times
  - if protective orders cannot be confirmed, emergency close position
- Log all actions

LIMIT_RETEST:

- TTL default 30 minutes
- cancel if TTL expires
- cancel if 15m candle closes back inside broken range
- cancel if market data stale
- cancel if spread/slippage worsens
- cancel if daily loss breached
- cancel if max open positions reached
- cancel if portfolio risk limit changes

Tests:

- market execution creates position + SL + TP
- limit retest pending order
- limit retest fill
- TTL cancel
- protective order missing => emergency close
- execution logs created

---

## PHASE 11 - Position Monitor + Kill Switch

Implement continuous monitor independent of LLM.

Responsibilities:

- monitor open positions
- monitor open orders
- verify protective orders
- process market updates for paper fills
- update unrealized PnL
- detect stale orders
- cancel expired limit orders
- detect broker/DB mismatch
- apply cooldown rules
- execute kill switch

Kill switch:

- daily loss uses realized + unrealized PnL
- if daily loss > max_daily_loss_pct:
  - cancel open orders
  - close open positions
  - halt new entries until daily reset
  - alert user

Tests:

- SL hit updates realized PnL
- TP hit updates realized PnL
- daily loss kill switch
- cooldown after consecutive losses
- stale order cancellation
- monitor continues when LLM unavailable

---

## PHASE 12 - Scheduler + Main Loop

Implement:

- run-once command
- run command
- 15m candle close trigger from WebSocket confirm event if available
- fallback time-based scheduler with 5s buffer
- no overlapping cycles
- cycle_id generation
- log every cycle

Main cycle:

1. Check bot state
2. Refresh universe if needed
3. Get market data health
4. Run universe scanner
5. Run setup scanner
6. Compute candidate scores
7. Identify LLM-eligible candidates
8. For each LLM-eligible candidate:
   - build compact context
   - call LLM veto
   - store raw and parsed decision
9. Collect all LLM-approved candidates
10. Run portfolio-level Risk Engine ranking
11. Execute only approved candidates within portfolio constraints
12. Log all decisions and skips

Skip reason examples:

- NO_CANDIDATE
- LLM_BLOCK
- LLM_BUDGET_EXCEEDED
- PORTFOLIO_RISK_LIMIT
- RISK_REJECTED
- DATA_STALE
- MARKET_DATA_UNHEALTHY

Tests:

- 0 eligible candidates => no LLM calls
- 5 eligible candidates => 5 LLM calls
- budget exceeded => remaining candidates BLOCK
- 5 LLM allows but max_new_positions_per_cycle=2 => only 2 execute
- run-once no candidate
- run-once LLM block
- run-once risk reject
- run-once approved executes paper order
- overlapping cycle skipped
- skipped candidates logged

---

## PHASE 13 - Manual / External Signal Source

Implement SignalSource architecture.

Signal sources:

- InternalStrategySignalSource
- ManualSignalSource
- WebhookSignalSource optional later
- TelegramSignalSource optional later only if compliant/safe

Implement:

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

CLI:

```bash
bot signal add --source cryptocium --symbol TRUMPUSDT --side LONG --entry-min 2.7 --entry-max 2.8 --sl 2.6 --tp1 3.0 --tp2 3.5
bot signal list --source cryptocium
bot signal stats --source cryptocium
```

Tests:

- manual signal accepted
- invalid RR rejected
- external watchlist updated
- external signal cannot bypass risk
- signal stats calculated

---

## PHASE 14 - Daily Reset + Reports + Reviewer

Daily reset:

- reset daily counters
- reconcile broker state with DB
- generate daily report
- backup DB/logs
- reset applicable cooldowns

Daily report:

- total trades
- win rate
- realized PnL
- unrealized PnL
- net PnL
- total fees
- total slippage
- max drawdown
- profit factor
- LLM decision breakdown
- reason code distribution
- source breakdown:
  - internal strategy
  - manual/external signals

Reviewer:

- every 50 trades: diagnostics
- every 200 trades: pattern analysis
- every 500 trades: strategy evaluation
- no auto strategy mutation

Reviewer output:

- recommendation only
- no live config mutation
- no automatic strategy change

Tests:

- daily report generated
- counters reset
- profit factor calculation
- reason code distribution
- source performance breakdown

---

## PHASE 15 - Alerts

Implement Telegram alert or generic webhook alert.

Alert events:

- bot started
- bot halted
- preflight failed
- market data stale
- WebSocket reconnect loop
- LLM fallback block spike
- LLM budget exceeded
- trade executed
- protective order failure
- emergency close
- daily loss kill switch
- portfolio risk limit spike
- broker/DB mismatch
- orphan position
- DB error
- live mode attempted without explicit enable flag

Tests:

- alert formatting
- alert send mock
- dangerous error triggers alert
- normal skipped trade does not spam alert

---

## PHASE 16 - Backtesting

Implement:

```bash
bot backtest --config configs/prod.yaml --from YYYY-MM-DD --to YYYY-MM-DD
```

Requirements:

- Uses historical candles from DB
- Runs same strategy/risk/executor logic
- Uses PaperBroker
- LLM configurable:
  - disabled: mock allow/block
  - enabled: call OpenRouter with cache
- No lookahead bias
- Conservative same-candle SL/TP assumption

Metrics:

- total trades
- win rate
- net PnL
- gross PnL
- max drawdown
- profit factor
- average R
- fee impact
- slippage impact
- setup type performance
- regime performance
- source performance
- LLM value-add
- portfolio risk rejection stats

Tests:

- backtest runs on sample data
- no lookahead bias
- SL-first assumption
- result stored
- LLM cache used when enabled

---

## PHASE 17 - Deployment

Provide:

- Dockerfile
- docker-compose.yml for app + PostgreSQL
- .env.example
- configs/paper.yaml
- configs/live.example.yaml
- systemd service
- VPS README
- local README
- backup script
- restore script
- migration command
- graceful shutdown behavior

Systemd:

- futures-bot.service
- restart policy
- working directory
- env file
- journalctl instructions

Graceful shutdown:

- stop scheduler
- persist state
- flush logs
- close DB
- do not corrupt state

Tests/checks:

- config validate
- migration run
- app starts in paper mode
- preflight command works

---

## PHASE 18 - Optional LiveBroker Adapter

Do not enable live by default.

Implement BybitLiveBroker behind Broker interface only if requested.

Safety requirements:

- app.mode must be live
- broker.provider must be bybit_live
- env ENABLE_LIVE_TRADING must be true
- config must explicitly set live_confirmed: true
- API key permissions must be read+trade, no withdraw
- preflight must detect open positions/orders
- protective orders must be verified
- emergency close must be implemented
- live mode must refuse to run if safety checks fail

Do not bypass PaperBroker architecture.
Do not let live broker change strategy/risk behavior.

Tests:

- live disabled without env
- live disabled without config confirmation
- order request mapping tests
- protective order failure path

---

## PHASE N - Safety Audit and Hardening

Perform full review:

1. Can LLM create trades? It must not.
2. Can LLM set arbitrary size? It must not.
3. Can LLM change side? It must not.
4. Can Risk Engine be bypassed? It must not.
5. Can stale data lead to trade? It must not.
6. Can position exist without SL? It must not.
7. Are all skips logged?
8. Are all raw LLM responses logged?
9. Does daily loss include unrealized PnL?
10. Is sizing deterministic?
11. Are WebSocket reconnects handled?
12. Is orderbook stale detection working?
13. Does bot recover after restart?
14. Does PaperBroker reconcile with DB?
15. Does live mode require explicit guards?
16. Are indicators minimal and not overfit?
17. Does dynamic LLM routing avoid sending raw all-pair data?
18. Are all LLM calls gated by candidate_score?
19. Is LLM budget cap enforced?
20. Are portfolio risk limits enforced after multiple LLM approvals?
21. Can many LLM approvals cause overexposure? It must not.
22. Are candidates skipped by portfolio risk logged?

Add missing tests.
Fix safety issues.
Produce final safety report.
