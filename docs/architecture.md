# BotSurv - Architecture & Planning Document

> Phase 0 Output. Planning documentation only. No production code.

---

## 1. Architecture Summary

BotSurv is a single-user, terminal-based crypto futures trading backend written in Go. It is designed as a modular, event-driven system with clear separation between market data, strategy, risk, execution, and broker layers.

### Core Architectural Principles

| Principle | Description |
|-----------|-------------|
| **WebSocket-first** | Real-time market data via WebSocket. REST is reserved for bootstrap, backfill, recovery, and reconciliation. |
| **Paper-first** | PaperBroker is the default. Live trading requires explicit multi-layer safety guards. |
| **LLM as veto only** | The LLM cannot create trades, change side, set entry/SL/TP/size, or override the Risk Engine. |
| **Risk Engine is final authority** | Every trade must pass deterministic risk validation. No exceptions. |
| **Less-is-more strategy** | Minimal indicator set to avoid noise and overfitting. |
| **Deterministic setup** | The Setup Engine calculates proposed trades using deterministic rules. |
| **Modular broker** | Strategy and Risk Engine are broker-agnostic. Only the Broker adapter changes between paper and live. |
| **Every skip is logged** | Every skipped trade must include explicit reason codes. |
| **No position without SL** | Every futures position must have a protective stop-loss. |

### High-Level Data Flow

```
+---------------+     +------------------+     +-------------------+
| MarketDataSvc | --> | Universe Scanner | --> | Setup Engine      |
| (WS + REST)   |     | (L1/L2/L3 funnel)|     | (deterministic)   |
+---------------+     +------------------+     +-------------------+
                                                       |
                                                       v
+---------------+     +------------------+     +-------------------+
|   Executor    | <-- |   Risk Engine    | <-- | LLM Veto Agent    |
| (Broker iface)|     | (final authority)|     | (ALLOW/REDUCE/BLOCK|
+---------------+     +------------------+     +-------------------+
       |
       v
+---------------+
| PaperBroker   |
| (simulation)  |
+---------------+
       |
       v
+---------------+
|   Position    |
|   Monitor     |
+---------------+
```

### Execution Path

1. MarketDataService maintains real-time state (candles, orderbook, trades, prices).
2. Universe Scanner filters the broad symbol universe through a 3-layer funnel.
3. Setup Engine runs deterministic breakout/retest logic on quality symbols.
4. Valid candidates receive a `candidate_score`.
5. Candidates with `candidate_score >= min_candidate_score` are eligible for LLM review.
6. Dynamic LLM Routing sends compact per-candidate context to the LLM.
7. LLM returns one of: `ALLOW_MARKET`, `ALLOW_LIMIT_RETEST`, `REDUCE_SIZE`, `BLOCK`.
8. Risk Engine validates the proposed trade against portfolio limits, market health, sizing, and safety invariants.
9. Executor places orders via the Broker interface.
10. Position Monitor continuously verifies protective orders, updates PnL, and enforces kill switches.

---

## 2. Component Boundaries

### Component Responsibilities and Interfaces

| Component | Responsibility | What it DOES NOT do |
|-----------|---------------|---------------------|
| **MarketDataService** | Maintain real-time market state via WebSocket. Provide cached candles, prices, orderbook, and trade flow. Detect stale data. Handle reconnect and backfill. | Execute trades. Make trading decisions. |
| **UniverseScanner** | Filter broad symbol universe into quality candidates using liquidity, volatility, and execution metrics. | Run strategy logic. Send data to LLM. |
| **SetupEngine** | Calculate deterministic proposed trade parameters (side, entry, SL, TP, RR, setup_type, invalidation_level, reason_codes) based on ATR, EMA200, range, and volume. Does NOT calculate position size. | Execute trades. Override Risk Engine. Change side. Calculate size. |
| **LLMVetoAgent** | Review proposed trades in compact context. Return ALLOW/REDUCE/BLOCK with reason codes. | Create trades. Set prices or size. Override Risk Engine. |
| **RiskEngine** | Final authority. Validate all safety invariants, portfolio limits, sizing, market health, and signal freshness. Approve or reject with reason codes. | Create trades. Modify strategy parameters. |
| **Executor** | Place orders via Broker interface. Ensure protective orders follow entry. Handle LIMIT_RETEST lifecycle. Retry protective order placement. | Decide whether to trade. Bypass Risk Engine. |
| **Broker (interface)** | Abstract order placement, position queries, and account state. PaperBroker simulates fills, fees, slippage, margin, and PnL. | Make trading decisions. |
| **PositionMonitor** | Continuously monitor open positions and orders. Update unrealized PnL. Enforce kill switches. Detect orphan positions or missing protective orders. | Create new entries. |
| **SignalSource (interface)** | Normalize internal strategy signals and external manual signals into ProposedTrade format. | Bypass Risk Engine or LLM. |
| **AlertService** | Send notifications for critical events (kill switch, protective order failure, emergency close, LLM budget exceeded). | Make trading decisions. |
| **Reporter** | Generate daily reports, trade statistics, and periodic diagnostics. No auto-strategy mutation. | Modify config or strategy. |

### Interface Contract Rule

Strategy, Risk Engine, LLM Veto Agent, and Executor must not know whether the broker is paper or live. Only the Broker adapter and the deployment config change.

---

## 3. Folder Structure

```
botsurv/
├── cmd/
│   └── bot/
│       └── main.go                 # CLI entry point only
├── internal/
│   ├── app/
│   │   ├── app.go                  # Application lifecycle: init, run, shutdown
│   │   ├── config.go               # Config struct + validation
│   │   └── state.go                # BotState, runtime flags
│   ├── cli/
│   │   ├── root.go                 # Cobra root command
│   │   ├── init.go                 # bot init
│   │   ├── preflight.go            # bot preflight
│   │   ├── run.go                  # bot run, bot run-once
│   │   ├── positions.go            # bot positions
│   │   ├── orders.go               # bot orders
│   │   ├── report.go               # bot report daily
│   │   ├── emergency.go            # bot emergency-close-all
│   │   ├── backtest.go             # bot backtest
│   │   ├── config.go               # bot config validate
│   │   ├── state.go                # bot state
│   │   ├── universe.go             # bot universe list/refresh/add/remove
│   │   └── signal.go               # bot signal add/list/stats
│   ├── domain/
│   │   ├── candle.go
│   │   ├── symbol.go
│   │   ├── orderbook.go
│   │   ├── trade_flow.go
│   │   ├── proposed_trade.go
│   │   ├── candidate.go
│   │   ├── llm_decision.go
│   │   ├── risk_decision.go
│   │   ├── order.go
│   │   ├── position.go
│   │   ├── execution.go
│   │   ├── account.go
│   │   ├── signal.go
│   │   ├── market_health.go
│   │   └── report.go
│   ├── marketdata/
│   │   ├── interface.go            # MarketDataService interface
│   │   ├── bybit_ws.go             # Bybit WebSocket implementation
│   │   ├── bybit_rest.go           # Bybit REST backfill/bootstrap
│   │   ├── postgres_history.go     # HistoricalMarketDataService for DB/backtest
│   │   ├── mock.go                 # MockMarketDataService (testing)
│   │   ├── candle_store.go         # In-memory candle cache + DB persistence
│   │   ├── orderbook_store.go      # Local orderbook snapshot/delta
│   │   └── trade_flow_store.go     # Rolling trade flow windows
│   ├── universe/
│   │   ├── scanner.go              # UniverseScanner orchestrator
│   │   ├── layer1.go               # All pairs light scanner
│   │   ├── layer2.go               # Market quality filter
│   │   └── layer3.go               # Setup scanner integration
│   ├── strategy/
│   │   ├── setup_engine.go         # Deterministic setup logic
│   │   ├── indicators.go           # ATR, EMA200, SMA volume, range
│   │   └── screener.go             # Candidate ranking and LLM eligibility
│   ├── llm/
│   │   ├── interface.go            # LLMClient interface
│   │   ├── openrouter.go           # OpenRouter client
│   │   ├── mock.go                 # MockLLMClient
│   │   ├── context_builder.go      # Compact context per candidate
│   │   └── validator.go            # Output schema validation
│   ├── risk/
│   │   ├── engine.go               # RiskEngine implementation
│   │   ├── portfolio.go            # Portfolio-level constraints
│   │   ├── sizing.go               # Position sizing logic
│   │   └── validator.go            # Per-trade validation rules
│   ├── broker/
│   │   ├── interface.go            # Broker interface
│   │   ├── paper.go                # PaperBroker implementation
│   │   └── bybit_live.go           # BybitLiveBroker (Phase 18 only)
│   ├── executor/
│   │   ├── executor.go             # Order execution orchestrator
│   │   └── limit_retest.go         # LIMIT_RETEST lifecycle manager
│   ├── monitor/
│   │   ├── monitor.go              # PositionMonitor
│   │   └── kill_switch.go          # Daily loss and emergency logic
│   ├── signal/
│   │   ├── interface.go            # SignalSource interface
│   │   ├── internal.go             # InternalStrategySignalSource
│   │   └── manual.go               # ManualSignalSource (CLI input)
│   ├── alert/
│   │   ├── interface.go            # AlertService interface
│   │   ├── telegram.go             # Telegram bot alert
│   │   └── webhook.go              # Generic webhook alert
│   ├── report/
│   │   ├── daily.go                # Daily report generation
│   │   └── reviewer.go             # Periodic diagnostics (no auto-mutation)
│   ├── db/
│   │   ├── connection.go           # PostgreSQL connection
│   │   ├── migrations.go           # Migration runner
│   │   ├── repositories.go         # Repository interfaces
│   │   └── postgres_impl.go        # PostgreSQL implementations
│   └── logger/
│       └── logger.go               # Structured JSON logger setup
├── configs/
│   ├── paper.yaml                  # Paper trading config
│   └── live.example.yaml           # Live config template (safety guards required)
├── scripts/
│   ├── backup.sh                   # DB/log backup
│   └── restore.sh                  # DB/log restore
├── deployments/
│   ├── Dockerfile
│   ├── docker-compose.yml
│   └── futures-bot.service         # systemd service example
├── docs/
│   ├── product-spec.md
│   ├── phases.md
│   ├── decisions.md
│   ├── ai-workflow.md
│   └── architecture.md             # This file
├── migrations/
│   └── *.sql                       # SQL migration files
├── .env.example
├── go.mod                          # (to be created in Phase 1)
└── README.md
```

---

## 4. Database Schema Draft

### Design Notes

- PostgreSQL is the only supported database.
- Repository interfaces keep business logic decoupled from SQL details, but repository implementations target PostgreSQL.
- Candles are persisted for backtesting and recovery.
- Trades, decisions, and reason codes are fully auditable.
- `reason_codes` and `risk_flags` are stored as JSON text in Phase 1. Later phases may move them to `JSONB` with indexes if query patterns require it.

### Tables

#### `candles`

| Column | Type | Notes |
|--------|------|-------|
| id | BIGSERIAL / INTEGER PK | |
| symbol | VARCHAR(32) | index |
| timeframe | VARCHAR(8) | e.g., 15m, 1H |
| open_time | BIGINT | Unix ms |
| open | DECIMAL(20,8) | |
| high | DECIMAL(20,8) | |
| low | DECIMAL(20,8) | |
| close | DECIMAL(20,8) | |
| volume | DECIMAL(20,8) | |
| turn_over | DECIMAL(20,8) | optional |
| confirmed | BOOLEAN | true when candle closed |
| UNIQUE(symbol, timeframe, open_time) | | |

#### `universe_symbols`

| Column | Type | Notes |
|--------|------|-------|
| id | SERIAL / INTEGER PK | |
| symbol | VARCHAR(32) | UNIQUE |
| status | VARCHAR(16) | trading, delisted, etc. |
| quote_asset | VARCHAR(8) | USDT |
| base_asset | VARCHAR(16) | |
| min_notional | DECIMAL(20,8) | |
| tick_size | DECIMAL(20,8) | |
| lot_size | DECIMAL(20,8) | |
| max_leverage | DECIMAL(5,2) | |
| blacklist | BOOLEAN | DEFAULT false |
| force_include | BOOLEAN | DEFAULT false |
| last_scan_at | TIMESTAMPTZ | |
| liquidity_score | DECIMAL(5,2) | 0-100 |
| created_at | TIMESTAMPTZ | DEFAULT NOW() |

#### `cycles`

| Column | Type | Notes |
|--------|------|-------|
| id | BIGSERIAL / INTEGER PK | |
| cycle_id | VARCHAR(36) | UUID or deterministic |
| started_at | TIMESTAMPTZ | |
| ended_at | TIMESTAMPTZ | |
| status | VARCHAR(16) | running, completed, failed |
| reason_codes | TEXT | JSON array string |

#### `candidates`

| Column | Type | Notes |
|--------|------|-------|
| id | BIGSERIAL / INTEGER PK | |
| cycle_id | VARCHAR(36) | FK to cycles |
| symbol | VARCHAR(32) | |
| candidate_score | DECIMAL(5,2) | 0-100 |
| liquidity_score | DECIMAL(5,2) | |
| execution_score | DECIMAL(5,2) | |
| setup_score | DECIMAL(5,2) | |
| volatility_score | DECIMAL(5,2) | |
| llm_eligible | BOOLEAN | |
| llm_routing_reason_codes | TEXT | JSON array string |
| regime | VARCHAR(16) | trend_up, trend_down, range, chop |
| setup_type | VARCHAR(32) | breakout, retest |
| side | VARCHAR(8) | LONG, SHORT |
| proposed_entry | DECIMAL(20,8) | |
| proposed_stop_loss | DECIMAL(20,8) | |
| proposed_take_profit | DECIMAL(20,8) | |
| rr | DECIMAL(5,2) | |
| expected_move | DECIMAL(20,8) | |
| estimated_total_cost | DECIMAL(20,8) | |

#### `llm_decisions`

| Column | Type | Notes |
|--------|------|-------|
| id | BIGSERIAL / INTEGER PK | |
| candidate_id | BIGINT | FK to candidates |
| cycle_id | VARCHAR(36) | |
| raw_response | TEXT | stored before parsing |
| decision | VARCHAR(32) | ALLOW_MARKET, ALLOW_LIMIT_RETEST, REDUCE_SIZE, BLOCK |
| confidence | DECIMAL(3,2) | |
| size_multiplier | DECIMAL(3,2) | 1.0, 0.75, 0.5, 0.25, 0.0 |
| regime | VARCHAR(16) | |
| reason_codes | TEXT | JSON array string |
| risk_flags | TEXT | JSON array string |
| notes | TEXT | max 2 sentences |
| validation_status | VARCHAR(16) | valid, invalid_json, invalid_enum, low_confidence, etc. |
| created_at | TIMESTAMPTZ | DEFAULT NOW() |

#### `risk_decisions`

| Column | Type | Notes |
|--------|------|-------|
| id | BIGSERIAL / INTEGER PK | |
| candidate_id | BIGINT | FK to candidates |
| cycle_id | VARCHAR(36) | |
| approved | BOOLEAN | |
| final_position_notional | DECIMAL(20,8) | |
| required_margin | DECIMAL(20,8) | |
| estimated_loss | DECIMAL(20,8) | |
| reason_codes | TEXT | JSON array string |
| portfolio_rank | INT | rank among approved candidates |
| portfolio_reject_reason | VARCHAR(64) | if rejected by portfolio limit |
| created_at | TIMESTAMPTZ | DEFAULT NOW() |

#### `orders`

| Column | Type | Notes |
|--------|------|-------|
| id | BIGSERIAL / INTEGER PK | |
| broker_order_id | VARCHAR(64) | nullable |
| position_id | BIGINT | FK to positions. NULL for entry orders before fill. Set after fill to link protective orders. |
| symbol | VARCHAR(32) | |
| side | VARCHAR(8) | BUY, SELL |
| order_type | VARCHAR(16) | MARKET, LIMIT, STOP_MARKET, TAKE_PROFIT_MARKET |
| qty | DECIMAL(20,8) | |
| price | DECIMAL(20,8) | nullable for market |
| stop_price | DECIMAL(20,8) | nullable |
| status | VARCHAR(16) | pending, filled, partially_filled, cancelled, rejected |
| created_at | TIMESTAMPTZ | DEFAULT NOW() |
| updated_at | TIMESTAMPTZ | |

#### `positions`

| Column | Type | Notes |
|--------|------|-------|
| id | BIGSERIAL / INTEGER PK | |
| symbol | VARCHAR(32) | |
| side | VARCHAR(8) | LONG, SHORT |
| entry_price | DECIMAL(20,8) | |
| size | DECIMAL(20,8) | |
| leverage | DECIMAL(5,2) | |
| margin | DECIMAL(20,8) | |
| stop_loss | DECIMAL(20,8) | NOT NULL |
| take_profit | DECIMAL(20,8) | |
| sl_order_id | BIGINT | FK to orders (STOP_MARKET). Used by monitor to verify protective stop exists after restart. |
| tp_order_id | BIGINT | FK to orders (TAKE_PROFIT_MARKET). Used by monitor to verify protective TP exists after restart. |
| unrealized_pnl | DECIMAL(20,8) | |
| realized_pnl | DECIMAL(20,8) | DEFAULT 0 |
| status | VARCHAR(16) | open, closed |
| source | VARCHAR(32) | internal_strategy, manual, webhook |
| opened_at | TIMESTAMPTZ | |
| closed_at | TIMESTAMPTZ | nullable |

#### `executions`

| Column | Type | Notes |
|--------|------|-------|
| id | BIGSERIAL / INTEGER PK | |
| order_id | BIGINT | FK to orders |
| symbol | VARCHAR(32) | |
| side | VARCHAR(8) | |
| qty | DECIMAL(20,8) | |
| price | DECIMAL(20,8) | |
| fee | DECIMAL(20,8) | |
| slippage | DECIMAL(20,8) | |
| executed_at | TIMESTAMPTZ | |

#### `account_state`

| Column | Type | Notes |
|--------|------|-------|
| id | BIGSERIAL / INTEGER PK | |
| balance | DECIMAL(20,8) | |
| available_balance | DECIMAL(20,8) | |
| used_margin | DECIMAL(20,8) | |
| equity | DECIMAL(20,8) | |
| realized_pnl | DECIMAL(20,8) | |
| unrealized_pnl | DECIMAL(20,8) | |
| total_fees | DECIMAL(20,8) | |
| total_slippage | DECIMAL(20,8) | |
| daily_loss | DECIMAL(20,8) | |
| recorded_at | TIMESTAMPTZ | DEFAULT NOW() |

#### `external_signals`

| Column | Type | Notes |
|--------|------|-------|
| id | BIGSERIAL / INTEGER PK | |
| source | VARCHAR(32) | e.g., cryptocium |
| symbol | VARCHAR(32) | |
| side | VARCHAR(8) | |
| entry_min | DECIMAL(20,8) | |
| entry_max | DECIMAL(20,8) | |
| stop_loss | DECIMAL(20,8) | |
| tp1 | DECIMAL(20,8) | |
| tp2 | DECIMAL(20,8) | nullable |
| signal_time | TIMESTAMPTZ | nullable |
| notes | TEXT | nullable |
| status | VARCHAR(16) | pending, converted, rejected, expired |
| created_at | TIMESTAMPTZ | DEFAULT NOW() |
| expires_at | TIMESTAMPTZ | |

#### `alerts`

| Column | Type | Notes |
|--------|------|-------|
| id | BIGSERIAL / INTEGER PK | |
| alert_type | VARCHAR(32) | |
| severity | VARCHAR(16) | info, warning, danger |
| message | TEXT | |
| sent_at | TIMESTAMPTZ | DEFAULT NOW() |
| delivered | BOOLEAN | DEFAULT false |

#### `backtest_runs`

| Column | Type | Notes |
|--------|------|-------|
| id | BIGSERIAL / INTEGER PK | |
| run_id | VARCHAR(36) | UUID |
| from_date | DATE | |
| to_date | DATE | |
| config_snapshot | JSONB / TEXT | |
| total_trades | INT | |
| win_rate | DECIMAL(5,2) | |
| net_pnl | DECIMAL(20,8) | |
| max_drawdown | DECIMAL(5,2) | |
| profit_factor | DECIMAL(5,2) | |
| created_at | TIMESTAMPTZ | DEFAULT NOW() |

---

## 5. Config YAML Draft

### `configs/paper.yaml`

```yaml
app:
  mode: paper                    # paper | live
  log_level: info
  json_logs: true
  cycle_interval_seconds: 900    # 15m; overridden by WS candle close event if available
  max_cycle_overlap: false
  timezone: UTC

database:
  driver: postgres
  dsn: "postgres://botsurv:botsurv@localhost:5432/botsurv?sslmode=disable"

market_data:
  provider: bybit_ws
  symbols:
    mode: all_usdt_perpetual     # all_usdt_perpetual | explicit_list
    explicit_list: []
  ws_url: "wss://stream.bybit.com/v5/public/linear"
  rest_url: "https://api.bybit.com"
  reconnect_interval_seconds: 5
  stale_data_threshold_seconds: 30
  backfill_candles: 500
  timeframes:
    - 15m
    - 1H
  orderbook_depth: 50
  trade_flow_windows:
    - 15
    - 60
    - 300

broker:
  provider: paper
  paper:
    starting_balance_usd: 1000.0
    fee_maker_bps: 2.0
    fee_taker_bps: 5.5
    slippage_model: fixed_bps     # fixed_bps | variable
    slippage_bps: 5.0
    default_leverage: 5

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
  filters:
    min_24h_volume_usd: 500000
    max_spread_bps: 50
    min_atr_health_score: 30

strategy:
  enabled: true
  timeframes:
    context: 1H
    setup: 15m
    execution: 5m
  indicators:
    atr_period: 14
    ema200_period: 200
    volume_sma_period: 20
    range_candles: 20
    min_volume_ratio: 1.3
    max_breakout_extension_atr: 2.5
    max_distance_from_breakout_atr: 1.0
    min_rr: 2.0
    expected_move_cost_multiplier: 3.0
  regime:
    trend_up_min_distance_from_ema_pct: 1.0
    trend_down_max_distance_from_ema_pct: -1.0
    range_max_distance_from_ema_pct: 0.5
  entry_types:
    - MARKET
    - LIMIT_RETEST
  limit_retest_ttl_minutes: 30

llm_routing:
  mode: dynamic
  min_candidate_score: 75
  max_calls_per_cycle: 0           # 0 = unlimited by count
  max_calls_per_day: 0             # 0 = unlimited by count
  max_cost_usd_per_day: 3.0
  require_execution_ok: true
  require_liquidity_ok: true
  hard_cap_candidates_per_cycle: 0 # 0 = no hard cap

llm:
  enabled: true
  provider: openrouter
  base_url: "https://openrouter.ai/api/v1"
  model: ""                        # REQUIRED. Example: "openai/gpt-4o-mini", "openai/gpt-5.5", "anthropic/claude-3.5-sonnet"
  temperature: 0
  timeout_seconds: 30
  max_tokens: 512
  budget:
    max_cost_usd_per_day: 3.0
    fallback_decision: BLOCK

portfolio_risk:
  max_open_positions: 3
  max_new_positions_per_cycle: 2
  max_total_exposure_usd: 300
  max_total_margin_used_pct: 50
  max_risk_per_trade_pct: 0.5
  max_daily_loss_pct: 3.0
  max_same_direction_positions: 2
  max_correlated_alt_positions: 2
  max_per_symbol_position: 1
  margin_per_trade_usd: 100
  max_leverage: 5
  min_notional_usd: 5
  cooldown_after_losses:
    enabled: true
    consecutive_losses: 3
    cooldown_minutes: 60

sizing:
  method: min_margin_or_risk
  margin_per_trade_usd: 100
  max_leverage: 5
  max_risk_per_trade_pct: 0.5

alerts:
  enabled: true
  provider: telegram               # telegram | webhook
  telegram:
    bot_token: ""                  # env: TELEGRAM_BOT_TOKEN
    chat_id: ""                    # env: TELEGRAM_CHAT_ID
  webhook:
    url: ""
  events:
    - bot_started
    - bot_halted
    - preflight_failed
    - market_data_stale
    - ws_reconnect_loop
    - llm_fallback_block_spike
    - llm_budget_exceeded
    - trade_executed
    - protective_order_failure
    - emergency_close
    - daily_loss_kill_switch
    - portfolio_risk_limit_spike
    - broker_db_mismatch
    - orphan_position
    - db_error
    - live_mode_attempted_without_guard

backtest:
  default_from: "2024-01-01"
  default_to: "2024-12-31"
  llm_mode: mock                   # mock | cached | live
```

### `configs/live.example.yaml`

Same as `paper.yaml` with these differences:

```yaml
app:
  mode: live
  live_confirmed: false            # MUST be set to true manually

broker:
  provider: bybit_live
  bybit_live:
    api_key: ""                    # env: BYBIT_API_KEY
    api_secret: ""                 # env: BYBIT_API_SECRET
    rest_url: "https://api.bybit.com"
    ws_url: "wss://stream.bybit.com/v5/private"
    testnet: false
```

---

## 6. Domain Model Draft

### Core Entities

#### Candle

```
Symbol      string
Timeframe   string
OpenTime    int64
Open        float64
High        float64
Low         float64
Close       float64
Volume      float64
TurnOver    float64
Confirmed   bool
```

#### SymbolInfo

```
Symbol          string
Status          string
QuoteAsset      string
BaseAsset       string
MinNotional     float64
TickSize        float64
LotSize         float64
MaxLeverage     float64
```

#### UniverseSymbol

```
SymbolInfo
Blacklist       bool
ForceInclude    bool
LiquidityScore  float64
LastScanAt      time.Time
```

#### ProposedTrade

```
Symbol              string
Side                Side        // LONG | SHORT
SetupType           string      // breakout | retest
Regime              string      // trend_up | trend_down | range | chop
EntryType           EntryType   // MARKET | LIMIT_RETEST
ProposedEntry       float64
ProposedStopLoss    float64
ProposedTakeProfit  float64
StopLossPct         float64
TakeProfitPct       float64
RR                  float64
InvalidationLevel   float64
ReasonCodes         []string
SetupScore          float64
ExpectedMove        float64
EstimatedTotalCost  float64
```

#### Candidate

```
ProposedTrade
CandidateScore              float64   // 0-100
LiquidityScore              float64   // 0-100
ExecutionScore              float64   // 0-100
SetupScore                  float64   // 0-100
VolatilityScore             float64   // 0-100
LLMEligible                 bool
LLMRoutingReasonCodes       []string  // why sent / why not sent
```

#### LLMDecision

```
Decision        string      // ALLOW_MARKET | ALLOW_LIMIT_RETEST | REDUCE_SIZE | BLOCK
Confidence      float64     // 0.0 - 1.0
SizeMultiplier  float64     // 1.0 | 0.75 | 0.5 | 0.25 | 0.0
Regime          string
ReasonCodes     []string
RiskFlags       []string
Notes           string      // max 2 sentences
RawResponse     string      // stored before parsing
ValidationStatus string     // valid | invalid_json | invalid_enum | low_confidence | ...
```

#### RiskDecision

```
Approved                bool
FinalPositionNotional   float64
RequiredMargin          float64
EstimatedLoss           float64
ReasonCodes             []string
PortfolioRank           int         // rank among approved candidates
PortfolioRejectReason   string      // if rejected by portfolio limit
```

#### Order

```
BrokerOrderID   string
Symbol          string
Side            OrderSide     // BUY | SELL
OrderType       OrderType     // MARKET | LIMIT | STOP_MARKET | TAKE_PROFIT_MARKET
Qty             float64
Price           *float64      // nil for market
StopPrice       *float64      // nil for non-stop
Status          OrderStatus
CreatedAt       time.Time
UpdatedAt       time.Time
```

#### Position

```
Symbol          string
Side            Side
EntryPrice      float64
Size            float64
Leverage        float64
Margin          float64
StopLoss        float64       // NOT NULL
TakeProfit      float64
UnrealizedPnL   float64
RealizedPnL     float64
Status          PositionStatus
Source          string        // internal_strategy | manual | webhook
OpenedAt        time.Time
ClosedAt        *time.Time
```

#### AccountState

```
Balance         float64
AvailableBalance float64
UsedMargin      float64
Equity          float64
RealizedPnL     float64
UnrealizedPnL   float64
TotalFees       float64
TotalSlippage   float64
DailyLoss       float64
RecordedAt      time.Time
```

#### MarketDataHealth

```
Symbol              string
Healthy             bool
LastUpdate          time.Time
StaleReason         string
CandlesHealthy      bool
OrderBookHealthy    bool
PriceHealthy        bool
```

#### Cycle

```
CycleID     string
StartedAt   time.Time
EndedAt     *time.Time
Status      string      // running | completed | failed
ReasonCodes []string
```

---

## 7. MarketDataService Design

### Interface

```go
type MarketDataService interface {
    Start(ctx context.Context) error
    Stop(ctx context.Context) error

    GetCandles(ctx context.Context, symbol, timeframe string, limit int) ([]Candle, error)
    GetLatestPrice(ctx context.Context, symbol string) (float64, error)
    GetOrderBookSummary(ctx context.Context, symbol string) (OrderBookSummary, error)
    GetTradeFlow(ctx context.Context, symbol string) (TradeFlow, error)

    IsHealthy(symbol string) bool
    LastUpdate(symbol string) time.Time
    HealthStatus(symbol string) MarketDataHealth
}
```

### BybitWSMarketDataService

**Responsibilities:**

- Connect to Bybit public WebSocket (linear perpetuals).
- Subscribe to:
  - `kline.15m` and `kline.1H` for all tracked symbols.
  - `tickers` for latest mark price.
  - `orderbook.50` for depth.
  - `publicTrade` for trade flow.
- On startup, use REST backfill:
  - 500 candles for 15m.
  - 500 candles for 1H.
- Persist closed/confirmed candles to DB.
- Maintain in-memory latest candle state per symbol/timeframe.
- Maintain local orderbook from snapshot + delta messages.
- Maintain trade flow rolling windows: 15s, 60s, 300s.
- Detect stale data per symbol (threshold configurable, default 30s).
- Auto-reconnect with exponential backoff (capped).
- Resubscribe to all channels after reconnect.
- Backfill missing candles after reconnect.

**Safety Rules:**

- WebSocket events update MarketDataService state only.
- Trading cycle reads from cache/DB. No direct trade execution from WS events.
- If market data is stale, Risk Engine must reject trade.
- Orderbook must reset on snapshot, apply deltas correctly.

### PostgreSQLHistoricalMarketDataService (Offline/Backtest)

- Serves historical candles from DB.
- No WebSocket connection.
- Used for backtesting and offline validation.

### MockMarketDataService (Testing)

- In-memory configurable state.
- Used for unit tests of Strategy, Risk, and Executor.

---

## 8. PaperBroker Design

### Interface

```go
type Broker interface {
    GetAccountState(ctx context.Context) (AccountState, error)
    GetOpenPositions(ctx context.Context) ([]Position, error)
    GetOpenOrders(ctx context.Context) ([]Order, error)
    PlaceOrder(ctx context.Context, req OrderRequest) (Order, error)
    CancelOrder(ctx context.Context, orderID string) error
    ClosePosition(ctx context.Context, symbol string) error
    EmergencyCloseAll(ctx context.Context) error
}
```

### PaperBroker Responsibilities

- Maintain simulated account state:
  - starting balance, available balance, used margin, equity.
  - realized PnL, unrealized PnL.
  - total fees, total slippage.
- Maintain open positions and open orders.
- Maintain closed positions history.

### Supported Order Types

- MARKET
- LIMIT
- STOP_MARKET
- TAKE_PROFIT_MARKET

### Paper Fill Behavior

- **Market order**: fills at latest price adjusted by configured slippage.
- **Limit order**: fills if candle high/low touches limit price.
- **SL (STOP_MARKET)**: triggers if candle crosses stop price.
- **TP (TAKE_PROFIT_MARKET)**: triggers if candle crosses take profit price.
- **Same-candle SL+TP**: conservative SL-first assumption by default.
- **Fees**: entry and exit fees applied per config (maker/taker bps).
- **Margin**: used margin tracked per position (notional / leverage).
- **PnL**: realized and unrealized PnL updated on every price tick.

### Safety Rules

- Position must never exist without SL.
- If protective SL/TP creation fails in paper state, emergency close position and log dangerous error.
- PaperBroker state is persisted to DB for recovery after restart.
- Reconcile paper state with DB on startup.

---

## 9. Universe Scanner Design

### 3-Layer Funnel

#### Layer 1 - All Pairs Light Scanner

- Fetch all available USDT perpetual symbols from Bybit public market metadata (REST).
- Filters:
  - status == `Trading`
  - quoteAsset == `USDT`
  - not delisted
  - not in blacklist
  - 24h volume >= `min_24h_volume_usd`
  - spread <= `max_spread_bps` (if available)
- Output: up to `light_scan_max_symbols` symbols, ranked by liquidity/activity.

#### Layer 2 - Market Quality Filter

- For each symbol from Layer 1, query MarketDataService for:
  - order book depth
  - estimated slippage for target notional
  - ATR health (ATR > 0, not abnormally low)
  - abnormal volatility check (optional)
  - funding window if available
- Output: up to `quality_filter_max_symbols` symbols with quality scores.

#### Layer 3 - Setup Scanner

- For each quality symbol, run Setup Engine:
  - 1H regime (EMA200 context)
  - 15m compression/range
  - breakout/retest detection
  - volume confirmation
  - RR potential
- Output: valid `ProposedTrade` candidates.

### Candidate Scoring

```text
candidate_score =
  liquidity_score  * 0.25
+ execution_score  * 0.25
+ setup_score      * 0.35
+ volatility_score * 0.15
```

All scores range 0-100.

### CLI Commands

- `bot universe list` - show current universe with scores.
- `bot universe refresh` - run full L1+L2+L3 scan.
- `bot universe add SYMBOL --force` - force-include symbol.
- `bot universe remove SYMBOL` - blacklist symbol.

---

## 10. Dynamic LLM Routing Design

### Principle

The bot does not use a hard `max_candidates_per_cycle` as the only gate. Any candidate that passes objective quality thresholds may be sent to LLM. Count is controlled by quality + budget, not by an arbitrary top-N.

### Eligibility Criteria

A candidate is eligible for LLM if ALL of the following are true:

1. `candidate_score >= llm_routing.min_candidate_score` (default 75)
2. Execution filters passed (spread, slippage, depth)
3. Liquidity filters passed
4. Setup Engine produced valid `ProposedTrade`
5. `RR >= min_rr`
6. `expected_move > 3 * estimated_total_cost`
7. `require_execution_ok == true` -> execution ok
8. `require_liquidity_ok == true` -> liquidity ok

### Budget Controls

| Config | Behavior |
|--------|----------|
| `max_calls_per_cycle: 0` | Unlimited by count per cycle |
| `max_calls_per_day: 0` | Unlimited by count per day |
| `max_cost_usd_per_day: 3.0` | Hard daily budget cap |

If budget cap is reached:
- Fallback decision = `BLOCK`
- Log `LLM_BUDGET_EXCEEDED`
- Remaining eligible candidates are blocked without API call

### Hard Cap (Optional)

`hard_cap_candidates_per_cycle: 0` means no hard cap. If set to N > 0, only top N candidates by `candidate_score` are sent, even if more are eligible.

### Observability

- Log `candidate_score` for every candidate.
- Log why candidate was sent to LLM (reason codes).
- Log why candidate was NOT sent to LLM (reason codes).
- Store raw LLM response before parsing.

### Context Builder

- One candidate = one LLM veto request.
- Do not dump raw candles.
- Compact JSON context includes:
  - candidate summary
  - 1H regime summary
  - 15m setup detail
  - optional 5m execution context
  - order book summary
  - trade flow summary (if enabled)
  - cost and economics
  - proposed trade
  - risk state
  - market data health

---

## 11. Portfolio Risk Control Design

### Portfolio-Level Constraints

| Constraint | Default | Purpose |
|------------|---------|---------|
| max_open_positions | 3 | Prevent overconcentration |
| max_new_positions_per_cycle | 2 | Limit cycle burst |
| max_total_exposure_usd | 300 | Hard exposure cap |
| max_total_margin_used_pct | 50 | Prevent over-leverage |
| max_risk_per_trade_pct | 0.5 | Per-trade risk |
| max_daily_loss_pct | 3.0 | Daily loss kill switch |
| max_same_direction_positions | 2 | Avoid directional bias |
| max_correlated_alt_positions | 2 | Correlation guard |
| max_per_symbol_position | 1 | No duplicate symbols |

### Portfolio Execution Logic

1. After LLM evaluation, collect all `ALLOW_MARKET` / `ALLOW_LIMIT_RETEST` / `REDUCE_SIZE` candidates.
2. Rank approved candidates by `candidate_score` (already composite: setup 35%, liquidity 25%, execution 25%, volatility 15%). No separate `final_score` is computed.
3. Starting from highest `candidate_score`, approve candidates one by one.
4. Before each approval, simulate portfolio state and check all constraints.
5. If any constraint would be breached, reject candidate with `PORTFOLIO_RISK_LIMIT`.
6. Continue until all candidates are evaluated or all limits are hit.

**Ranking formula**: `candidate_score` is the single ranking key. `portfolio_rank` is assigned only to candidates that pass portfolio constraints, in descending `candidate_score` order.

### Skip Logging

Every candidate skipped due to portfolio risk must log:
- `portfolio_rank`
- `portfolio_reject_reason` (e.g., MAX_OPEN_POSITIONS, MAX_EXPOSURE, MAX_SAME_DIRECTION)

---

## 12. Less-Is-More Strategy Design

### Philosophy

Scan broad universe, but trade only simple, high-quality setups. Avoid indicator stacking and overfitting. Every rule must have a clear purpose: liquidity, volatility, regime, setup, execution, or risk.

### Allowed Core Indicators

| Indicator | Purpose | Usage |
|-----------|---------|-------|
| **ATR(14)** | Volatility health, stop buffer, breakout extension check, price drift validation | Used for sizing stops and validating breakout size |
| **EMA200(1H)** | Regime/bias only | Not an entry trigger. Determines trend_up / trend_down / range / chop |
| **15m range high/low** | Last 20 candles | Breakout/retest setup detection |
| **Volume ratio** | Current vs SMA20 | Breakout confirmation only (must be >= 1.3x) |
| **Execution metrics** | Spread, depth, slippage estimate, depth_to_position_size_ratio | Entry quality and risk validation |

### Optional (Disabled by Default)

- Order flow rolling buy/sell ratio

### Explicitly Disallowed as Entry Indicators

- RSI
- MACD
- Stochastic
- Ichimoku
- EMA crossover
- Bollinger Bands as signal
- Formal candlestick pattern recognition
- Fibonacci auto-trading
- Complex sentiment/news-based entry

### New Indicator Policy

If a new indicator is added later, it must be:
1. Behind a feature flag.
2. Evaluated with A/B paper trading before enabling.

### Setup Types

1. **Breakout**: Price closes outside 15m 20-candle range with volume confirmation.
2. **Retest**: Price broke out but is within 1 ATR of breakout level; propose LIMIT_RETEST entry.

### Setup Validation Rules

- Breakout candle body ratio must be healthy.
- Breakout candle not too extended: `< 2.5 * ATR`.
- Distance from breakout level: if `> 1 * ATR`, propose `LIMIT_RETEST` instead of `MARKET`.
- `expected_move > 3 * total_cost_estimate`.
- `RR >= 1:2`.
- Volume > 1.3x SMA20 volume.

---

## 13. External Signal Source Design

### SignalSource Interface

```go
type SignalSource interface {
    Name() string
    GetSignals(ctx context.Context) ([]ExternalSignal, error)
    ConvertToProposedTrade(sig ExternalSignal) (ProposedTrade, error)
}
```

### Supported Sources

| Source | Phase | Notes |
|--------|-------|-------|
| InternalStrategySignalSource | Phase 8 | Deterministic setup engine output |
| ManualSignalSource | Phase 13 | CLI input |
| WebhookSignalSource | Later | Optional |
| TelegramSignalSource | Later | Only if compliant/safe |

### ManualSignalSource

- User inputs external signal via CLI.
- Required fields: `symbol`, `side`, `entry_min`, `entry_max`, `stop_loss`, `tp1`
- Optional fields: `tp2`, `signal_time`, `notes`
- Source name example: `cryptocium`
- Bot converts manual signal into `ProposedTrade` using the same deterministic conversion rules as internal signals.
- Manual signals flow through the **exact same pipeline** as internal strategy signals:
  1. Converted to `ProposedTrade`.
  2. Evaluated by Setup Engine (setup_score, candidate_score computed from market data at conversion time).
  3. If `candidate_score >= min_candidate_score`, sent to LLM Veto Agent.
  4. Risk Engine validates (market health, sizing, portfolio limits, daily loss).
  5. Executor places orders only if Risk Engine approves.
- **LLM behavior**: If `llm.enabled == true`, manual signals are treated identically to internal signals. They are NOT auto-approved. If the candidate does not meet `min_candidate_score`, it is blocked with reason `CANDIDATE_SCORE_TOO_LOW` and NOT sent to LLM. If LLM is disabled, the signal proceeds to Risk Engine directly.
- PaperBroker executes only if approved.
- External signal added to `external_signal_watchlist` with TTL (default 48h).

### Safety Rules

- External signal cannot bypass Risk Engine.
- External signal cannot bypass LLM veto. If LLM is enabled, the signal must produce a `candidate_score >= min_candidate_score` and receive an LLM decision other than `BLOCK`. If LLM is disabled, the signal still must pass all Risk Engine checks.
- External signal cannot bypass portfolio risk limits.
- External signal cannot bypass daily loss limit.
- External signal cannot bypass market data health checks.
- External signal cannot bypass the sizing formula; size is always computed by Risk Engine.

---

## 14. Risk Engine Design

### Input

- `UserConfig`
- `AccountState`
- `ProposedTrade`
- `LLMDecision`
- `CurrentMarketState` (from MarketDataService)
- `BotState`
- `CurrentPortfolioState`

### Output

- `APPROVED` or `REJECTED`
- `final_position_notional`
- `required_margin`
- `estimated_loss`
- `reason_codes` (array)
- `portfolio_rank`
- `portfolio_reject_reason`

### Validation Checklist (Deterministic)

#### Per-Trade Validations

1. LLM decision is valid (not BLOCK, valid enum, confidence >= 0.6)
2. Signal freshness valid
3. Market data not stale
4. Price drift acceptable
5. Spread acceptable
6. Depth acceptable
7. Slippage estimate acceptable
8. Not in cooldown period
9. No conflicting open orders
10. SL and TP on correct side
11. `RR >= configured min_rr` (default 1:2)
12. `expected_move > 3 * estimated_total_cost`
13. `min_notional` satisfied
14. Leverage valid
15. Liquidation price not too close

#### Portfolio-Level Validations

16. Daily loss not breached
17. Max open positions not breached
18. Max new positions per cycle not breached
19. Max total exposure not breached
20. Max total margin used not breached
21. Max same-direction positions not breached
22. Max correlated alt positions not breached
23. Max per-symbol position not breached
24. No duplicate symbol position (unless explicitly enabled)
25. Broker state consistent

### Sizing Formula

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

### Rejection Behavior

- If ANY validation fails: skip trade, log reason codes, alert if dangerous.
- If multiple LLM-approved candidates exist: rank by `candidate_score`, approve only best within portfolio constraints, skip rest with `PORTFOLIO_RISK_LIMIT`.

---

## 15. LLM Veto Agent Design

### Role

The LLM is a **veto/context agent only**. It reviews a compact, deterministic context for each proposed trade and returns one of four decisions.

### What LLM CANNOT Do

- Create new trades
- Change trade side
- Set entry price
- Set stop-loss price
- Set take-profit price
- Set arbitrary position size
- Override Risk Engine

### Allowed Decisions

| Decision | Meaning |
|----------|---------|
| `ALLOW_MARKET` | Approve market entry at proposed price |
| `ALLOW_LIMIT_RETEST` | Approve limit retest entry at proposed level |
| `REDUCE_SIZE` | Approve but reduce size by size_multiplier |
| `BLOCK` | Reject trade |

### Allowed Size Multipliers

- `1.0`, `0.75`, `0.5`, `0.25`, `0.0`

### Output Schema (JSON)

```json
{
  "decision": "ALLOW_MARKET | ALLOW_LIMIT_RETEST | REDUCE_SIZE | BLOCK",
  "confidence": 0.85,
  "size_multiplier": 1.0,
  "regime": "trend_up | trend_down | range | chop",
  "reason_codes": ["SETUP_QUALITY_OK", "VOLUME_CONFIRMATION"],
  "risk_flags": [],
  "notes": "Breakout confirmed with volume. Risk/reward acceptable."
}
```

### Validation & Fallback Rules

| Condition | Fallback |
|-----------|----------|
| Invalid JSON | BLOCK |
| Invalid decision enum | BLOCK |
| Invalid size_multiplier | BLOCK |
| Confidence < 0.6 | BLOCK |
| API error | BLOCK |
| Timeout | BLOCK |
| Rate limit | BLOCK |
| Budget exceeded | BLOCK + log `LLM_BUDGET_EXCEEDED` |

### Observability

- Raw response stored BEFORE parsing.
- Parsed decision stored.
- Validation status stored.
- LLM failure must not stop position monitoring.
- LLM failure only skips new trade.

### OpenRouter Integration

- Base URL: configurable, default `https://openrouter.ai/api/v1`
- API key: env `OPENROUTER_API_KEY`
- Model: configurable
- Optional headers: `HTTP-Referer`, `X-Title`
- Temperature: `0`
- Timeout: configurable

---

## 16. Failure Mode Table

| # | Failure Mode | Detection | Immediate Action | Recovery | Alert |
|---|-------------|-----------|------------------|----------|-------|
| 1 | WebSocket disconnect | Connection error / heartbeat timeout | Stop reads, mark data stale | Auto-reconnect + resubscribe + backfill | warning |
| 2 | Stale market data | LastUpdate > threshold | Reject trades for affected symbol | Reconnect or backfill | warning |
| 3 | Orderbook desync | Delta sequence gap | Reset orderbook, request snapshot | Apply new snapshot | info |
| 4 | LLM API error / timeout | HTTP error / timeout | Fallback BLOCK | Retry next cycle | warning |
| 5 | LLM invalid JSON | Parse error | Fallback BLOCK | Log raw response | warning |
| 6 | LLM budget exceeded | Daily cost > cap | Fallback BLOCK for remaining candidates | Reset at midnight UTC | info |
| 7 | Risk Engine rejects trade | Validation failure | Skip trade, log reason codes | None needed | info (danger if repeated) |
| 8 | Portfolio risk limit hit | Constraint breach | Skip candidate with PORTFOLIO_RISK_LIMIT | Evaluate next cycle | info |
| 9 | Protective order fails | Broker error / no confirmation | Emergency close position | Retry protective order up to 3x | danger |
| 10 | Orphan position (no SL) | Monitor detects missing SL | Emergency close position | Reconcile state | danger |
| 11 | Daily loss kill switch | Realized + unrealized PnL < -max_daily_loss_pct | Cancel orders, close positions, halt new entries | Reset at daily reset | danger |
| 12 | PaperBroker state mismatch | DB vs memory inconsistency | Reconcile from DB | Restart monitor | warning |
| 13 | DB connection failure | Query error | Halt new entries. Continue position monitoring where safe without new state writes. Alert user. | Reconnect to DB. Resume trading only after DB is healthy and state is reconciled. | danger |
| 14 | Config validation failure | Preflight check | Prevent startup | Fix config | warning |
| 15 | Live mode without guards | `app.mode=live` but missing env/config | Refuse to start | Set explicit guards | danger |
| 16 | Same-candle SL+TP | Monitor logic | Conservative SL-first assumption | None needed | info |
| 17 | LIMIT_RETEST TTL expiry | Time check | Cancel pending order | None needed | info |
| 18 | LIMIT_RETEST invalidation | Price back inside range | Cancel pending order | None needed | info |
| 19 | Cooldown active | Consecutive losses check | Reject new trades for symbol/direction | Wait for cooldown expiry | info |
| 20 | Broker/DB mismatch | Reconciliation check | Alert, attempt reconcile | Manual review if persists | danger |

---

## 17. Implementation Roadmap (Phase 1 to Phase N)

### Phase 1 - Foundation
**Goal:** Project skeleton, config, DB, domain models, CLI skeleton.
**Key Deliverables:**
- Go module, folder structure, CLI commands (`init`, `config validate`, `state`)
- YAML config loader with validation
- PostgreSQL connection layer and repository interfaces
- Migration runner
- Structured JSON logger
- All core domain models (Candle, SymbolInfo, ProposedTrade, Candidate, LLMDecision, RiskDecision, Order, Position, AccountState, etc.)

### Phase 2 - MarketDataService WebSocket Core
**Goal:** Real-time candle and price data from Bybit WebSocket.
**Key Deliverables:**
- MarketDataService interface
- BybitWSMarketDataService (public WS)
- Candle close detection and persistence
- In-memory candle cache
- REST backfill (500 candles 15m + 1H)
- Auto-reconnect and resubscribe
- Stale data detection

### Phase 3 - Orderbook + Trade Flow
**Goal:** Liquidity and execution quality metrics.
**Key Deliverables:**
- Orderbook snapshot/delta parsing
- Local orderbook maintenance
- Spread, depth, slippage estimation
- Trade flow rolling windows (15s, 60s, 300s)
- Buy/sell ratio computation

### Phase 4 - Universe Scanner
**Goal:** Funnel-based symbol filtering.
**Key Deliverables:**
- Layer 1: All pairs light scanner
- Layer 2: Market quality filter
- Layer 3: Setup scanner integration
- Candidate scoring and LLM eligibility
- CLI commands (`universe list`, `refresh`, `add`, `remove`)

### Phase 5 - PaperBroker Production
**Goal:** Realistic futures simulation.
**Key Deliverables:**
- Broker interface
- PaperBroker with account state, positions, orders
- Market/Limit/Stop/TP order fill simulation
- Fee, slippage, margin, PnL tracking
- Same-candle SL-first conservative logic
- Emergency close on missing protective orders

### Phase 6 - Risk Engine
**Goal:** Deterministic safety and portfolio authority.
**Key Deliverables:**
- Per-trade validation (25 checks)
- Portfolio-level validation
- Sizing logic (margin vs risk)
- REDUCE_SIZE multiplier support
- Ranking and portfolio capping
- Comprehensive reason codes

### Phase 7 - LLM OpenRouter Veto
**Goal:** Safe, observable LLM integration.
**Key Deliverables:**
- LLMClient interface
- OpenRouterClient with strict validation
- MockLLMClient for testing
- Output schema validation
- Fallback BLOCK behavior
- Raw response persistence
- Budget/cost tracking

### Phase 8 - Strategy / Setup Engine
**Goal:** Deterministic trade proposals.
**Key Deliverables:**
- ATR, EMA200, SMA volume, range calculations
- 1H regime detection
- 15m breakout/retest detection
- Volume confirmation
- ProposedTrade generation with setup_score

### Phase 9 - Screener + Context Builder
**Goal:** Candidate ranking and LLM context assembly.
**Key Deliverables:**
- Screener: rank candidates, mark LLM eligibility
- Context Builder: compact JSON per candidate
- Dynamic routing based on candidate_score
- Observability (score logging, reason codes)

### Phase 10 - Executor
**Goal:** Safe order placement and protective order lifecycle.
**Key Deliverables:**
- Executor using Broker interface
- Market entry + SL + TP placement
- LIMIT_RETEST lifecycle (TTL, invalidation, cancellation)
- Protective order retry (up to 3x)
- Emergency close on protective order failure

### Phase 11 - Position Monitor + Kill Switch
**Goal:** Continuous safety monitoring independent of LLM.
**Key Deliverables:**
- Open position/order monitoring
- Unrealized PnL updates
- Protective order verification
- Daily loss kill switch
- Cooldown enforcement
- Stale order cancellation
- Broker/DB mismatch detection

### Phase 12 - Scheduler + Main Loop
**Goal:** End-to-end trading cycle orchestration.
**Key Deliverables:**
- `run` and `run-once` commands
- 15m candle close trigger (WS event preferred, time-based fallback)
- Cycle orchestration (scan -> setup -> LLM -> risk -> execute)
- Skip reason logging
- Non-overlapping cycle guard

### Phase 13 - Manual / External Signal Source
**Goal:** External signal integration.
**Key Deliverables:**
- SignalSource interface
- InternalStrategySignalSource
- ManualSignalSource (CLI input)
- External signal watchlist with TTL
- CLI commands (`signal add`, `list`, `stats`)

### Phase 14 - Daily Reset + Reports + Reviewer
**Goal:** Operational reporting and diagnostics.
**Key Deliverables:**
- Daily counter reset
- Broker/DB reconciliation
- Daily report (PnL, win rate, fees, slippage, drawdown)
- Periodic reviewer (50/200/500 trade diagnostics, recommendations only)
- Source performance breakdown

### Phase 15 - Alerts
**Goal:** Operational notifications.
**Key Deliverables:**
- AlertService interface
- Telegram and webhook implementations
- Alert events mapping
- Severity-based alerting
- Anti-spam for normal skips

### Phase 16 - Backtesting
**Goal:** Historical strategy validation.
**Key Deliverables:**
- `bot backtest` command
- Historical candle replay
- Same strategy/risk/executor logic
- Configurable LLM mode (mock/cached/live)
- No lookahead bias
- Conservative same-candle SL-first assumption
- Performance metrics

### Phase 17 - Deployment
**Goal:** Production deployment artifacts.
**Key Deliverables:**
- Dockerfile
- docker-compose.yml (app + PostgreSQL)
- .env.example
- configs/paper.yaml and live.example.yaml
- systemd service
- VPS and local README
- Backup and restore scripts
- Migration command
- Graceful shutdown behavior

### Phase 18 - Optional LiveBroker Adapter
**Goal:** Live trading capability (explicitly guarded).
**Key Deliverables:**
- BybitLiveBroker behind Broker interface
- Multi-layer safety guards (env + config + permissions)
- Preflight open position/order detection
- Protective order verification
- Emergency close

### Phase N - Safety Audit and Hardening
**Goal:** Final safety verification.
**Key Deliverables:**
- Full 22-point safety checklist review
- Missing tests added
- Safety issues fixed
- Final safety report

---

## Appendix A: Safety Invariants Checklist

- [ ] LLM cannot create trades.
- [ ] LLM cannot change trade side.
- [ ] LLM cannot set entry, SL, TP, or arbitrary size.
- [ ] Risk Engine is final authority and cannot be bypassed.
- [ ] Setup Engine calculates proposed trade deterministically.
- [ ] Every skipped trade has explicit reason codes.
- [ ] No futures position exists without protective stop-loss.
- [ ] WebSocket-first market data; REST only for bootstrap/backfill/recovery.
- [ ] PaperBroker is default; live requires explicit multi-layer guards.
- [ ] Strategy uses minimal indicators only (ATR, EMA200, range, volume, execution metrics).
- [ ] Dynamic LLM routing is candidate_score based, not fixed top-N.
- [ ] Portfolio risk limits prevent overexposure even with many LLM approvals.
- [ ] Every cycle is logged, including skipped trades.
- [ ] Raw LLM responses are stored before parsing.
- [ ] LLM budget cap is enforced with fallback BLOCK.
- [ ] Daily loss includes realized + unrealized PnL.
- [ ] Sizing is deterministic (min of margin-based and risk-based).
- [ ] WebSocket reconnects are handled with resubscribe and backfill.
- [ ] Orderbook stale detection is operational.
- [ ] Bot recovers state after restart via DB reconciliation.
- [ ] Live mode requires explicit `ENABLE_LIVE_TRADING` env + `live_confirmed: true` config.
- [ ] External signals cannot bypass Risk Engine or LLM veto.
