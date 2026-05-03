# Engineering Decisions

## Decision 1 - LLM is veto-only

LLM cannot:

- create trades
- change side
- set entry
- set SL
- set TP
- set arbitrary size
- override Risk Engine

Reason:
Trading execution must remain deterministic, auditable, and bounded.

---

## Decision 2 - Risk Engine is final authority

Even if LLM allows a trade, Risk Engine can reject it.

Reason:
Safety, exposure control, stale data handling, and account protection must be deterministic.

---

## Decision 3 - WebSocket-first market data

Market data is WebSocket-first.

REST is used only for:

- bootstrap
- backfill
- recovery
- reconciliation

Reason:
Bot needs fresh market state but must still recover after disconnect.

---

## Decision 4 - PaperBroker first

PaperBroker is the default broker.

Live broker is optional and must be explicitly enabled later.

Reason:
Strategy and execution logic must be validated with paper trading before live capital.

---

## Decision 5 - Less-is-more indicators

Initial strategy only uses:

- ATR
- EMA200 1H
- 15m range high/low
- volume ratio
- execution metrics

Reason:
Avoid indicator noise, overfitting, and unclear edge attribution.

---

## Decision 6 - Dynamic LLM routing

The bot does not use a hard top-3 candidate limit as the only gate.

Any candidate that passes deterministic quality thresholds may be sent to LLM.

Reason:
Do not miss high-quality opportunities just because of a fixed candidate limit.

---

## Decision 7 - Portfolio risk controls limit execution

Even if many candidates are approved by LLM, Risk Engine enforces:

- max open positions
- max new positions per cycle
- max exposure
- max same-direction positions
- max per-symbol position

Reason:
Prevent overexposure when many assets move together.

---

## Decision 9 - Fibonacci is optional context, not entry trigger

Fibonacci retracement levels (0.5, 0.618, 0.786) are computed as optional confluence context only.

They are:
- Only used in LLM context JSON for veto/awareness
- Feature-flagged via `watchlist_context.enabled` and `watchlist_context.fibonacci.enabled`
- Capped by `scoring_weight` so they can never dominate the deterministic score
- Not allowed to modify entry, SL, TP, or side
- Not required for candidate eligibility

The watchlist context struct also provides chart quality, volume liquidity, narrative sector, and marketcap class labels.

All fields default to "unknown" / delta 0 when config is missing.

Reason:
Fibonacci can add useful pullback context for LLM veto decisions but must remain an optional
confluence layer, never a direct entry trigger. Validation must come from paper/backtest before
increasing weight.

---

## Decision 10 - External signals are inputs, not authority

Manual/external signals such as Telegram signals can be added.

But they cannot bypass:

- Setup conversion
- LLM veto
- Risk Engine
- portfolio limits
- daily loss limit

Reason:
External signals must be benchmarked and risk-managed.
