# Phase 1: Data Validation + Hard Blocks

## What Was Added

- `internal/marketdata/validator.go`
  - Candle batch validation gate for OHLCV sanity, minimum count, monotonic timestamps, gap detection, staleness, and closed/in-progress handling.
- `internal/risk/hard_blocks.go`
  - Pure `BlockIf*` functions for hard safety checks.
- `internal/risk/block_evaluator.go`
  - Single evaluator that runs all hard blocks and returns structured block output.
- `internal/screener/screener.go`
  - Wired candle validator at candidate-entry path before indicator/regime/setup logic.
- `internal/scheduler/scheduler.go`
  - Wired hard block evaluator before LLM calls.

## Configuration

Added in config:

```yaml
data_validation:
  min_candles: 250
  max_data_age_seconds:
    "1m": 30
    "5m": 90
    "15m": 180
    "1H": 600
    "4H": 1800

hard_blocks:
  max_spread_pct: 0.15
  max_funding_abs_pct: 0.5
  btc_flash_crash_5m_pct: -2.5
  daily_max_loss_pct: 3.0
  max_consecutive_losses: 3
  cooldown_after_loss_min: 30
  cooldown_after_win_min: 10
```

Runtime defaults are applied if fields are omitted.

## Example Block Log

```json
{
  "level":"info",
  "message":"candidate blocked by hard block evaluator",
  "symbol":"BTCUSDT",
  "blocks_triggered":["BlockIfStaleData","BlockIfSpreadTooWide"],
  "first_block":"market data is 142s old, max allowed 90s",
  "all_block_reasons":[
    "market data is 142s old, max allowed 90s",
    "spread too wide: 0.2200% > 0.1500%"
  ]
}
```
