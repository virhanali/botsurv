## Ringkasan Perubahan BotSurv v2 (Hari Ini)

### 1. DeepSeek LLM Provider (Baru)
- **File**: `internal/llm/client.go`
- **Provider**: `deepseek` (sebelumnya hanya `openrouter`)
- **Model**: `deepseek-v4-pro`
- **Request**:
  - `reasoning_effort: "max"`
  - `thinking: {"type":"enabled"}`
  - **Tidak** mengirim `temperature/top_p/presence_penalty/frequency_penalty`
- **Response parsing**:
  - Parse decision hanya dari `choices[0].message.content`
  - `reasoning_content` diabaikan, tidak dipersist
  - `RawResponse` = final content (bukan chain-of-thought)
- **Fail-fast validation**: Config validate cek `DEEPSEEK_API_KEY` env var
- **Fallback**: Jika API key hilang, return `BLOCK` dengan `MISSING_API_KEY`

### 2. LLM Call Caps (Scheduler)
- **File**: `internal/scheduler/scheduler.go`
- **Cap fields**:
  - `hard_cap_candidates_per_cycle`: 1
  - `max_calls_per_cycle`: 1
  - `max_calls_per_day`: 10
- **Effective cap** = strictest positive dari ketiga di atas (0 = unlimited)
- Sort by `candidate_score` descending sebelum truncation
- **Skip reasons**:
  - `LLM_HARD_CAP` — dibatasi oleh hard cap
  - `LLM_CALL_CAP_PER_CYCLE` — melebihi max per cycle
  - `LLM_CALL_CAP_PER_DAY` — melebihi max per day

### 3. Universe Scanning (All Coins)
- **Sebelum**: Hanya `BTCUSDT` (explicit mode)
- **Sekarang**: `all_usdt_perpetual` (651 instruments)
- `core_symbols: []` (kosong)
- `force_include_symbols: []` (kosong)
- Semua coin yang lolos engine bisa jadi candidate ke LLM

### 4. DB Schema Changes
- **Migration 005** (`migrations/005_widen_order_type.sql`):
  - `orders.order_type` `VARCHAR(16)` → `VARCHAR(32)` (untuk `TAKE_PROFIT_MARKET`)
- **Migration 006** (`migrations/006_audit_recovery_state.sql`):
  - Add `risk_decisions` table
  - Add `llm_usage_daily` table
  - Add `intended_stop_loss/intended_take_profit` ke `orders`
- **Domain**: `LLMDecision` punya field `CandidateID` (populated by repo on read)

### 5. Raw Response Preservation
- `LLMDecision.RawResponse` di-populate bahkan saat fallback BLOCK
- Fallback triggers: malformed JSON, invalid enum, low confidence (< 0.6)
- Validation status: `fallback` untuk keputusan fallback

### 6. E2E Tests
- `TestEndToEnd_PaperTradingCycle`: Full cycle dengan mock Bybit REST server
- `TestEndToEnd_Postgres`: Gated by `BOTSURV_TEST_POSTGRES_DSN`
- Assert DB persistence:
  - cycles, candidates, llm_decisions
  - orders (MARKET, STOP_MARKET, TAKE_PROFIT_MARKET)
  - positions, executions, account_snapshots
- **Tidak boleh** ada `UNIVERSE_REFRESH_FAILED`

### 7. Config Baru: `configs/paper.deepseek.yaml`
- Paper mode + DeepSeek + Bybit WS
- Universe: `all_usdt_perpetual`
- Margin per trade: $10
- Max leverage: 5
- LLM caps: hard_cap=1, per_cycle=1, per_day=10
- Alerts: **disabled** (tidak perlu Telegram env vars)

### 8. VPS Optimizations (Sudah Diterapkan)
- **Syslog**: 26GB → truncated
- **PostgreSQL tuning**:
  - `shared_buffers`: 128MB → 4GB
  - `effective_cache_size`: 4GB → 12GB
  - `work_mem`: 4MB → 16MB
  - `max_connections`: 100 → 20
- **journald**: `SystemMaxUse=500M`, `MaxFileSec=1week`
- **logrotate**: `/var/log/botsurv/*.log` daily rotate 14 hari
- **Service**: `botsurv-paper` enabled (auto-start on boot)
- **Monitor script** (`/opt/botsurv/monitor.sh`): Auto-detect config, disk check, Postgres readiness, LLM usage tracking, WS disconnect health

### 9. Deploy Workflow Fix
- `.github/workflows/deploy-paper.yml`: Validate & init pakai `paper.deepseek.yaml` (bukan `paper.vps.yaml`)
- `deploy/systemd/botsurv-paper.service`: ExecStart pakai `paper.deepseek.yaml`

### 10. API Key Validation
- Config validate cek env vars:
  - `OPENROUTER_API_KEY` (jika provider=openrouter)
  - `DEEPSEEK_API_KEY` (jika provider=deepseek)
- TestMain hanya set dummy key jika env var belum ada (fix live test)

---

## Hal yang Dihapus / Tidak Lagi Digunakan

1. **BTCUSDT-only restriction** — tidak ada hardcode BTCUSDT
2. **`bad_execs` check** (order_id=0) — dihapus dari monitor
3. **Explicit symbol list** — `explicit_list` dikosongkan
4. **Hybrid universe mode** — diganti `all_usdt_perpetual`
5. **Old monitor.sh** — diganti versi baru
6. **`AGENTS.md`** — diganti `OPENCLAW_PROMPT.md`

---

## Files Changed (Commit List)

1. `migrations/005_widen_order_type.sql` — new
2. `migrations/006_audit_recovery_state.sql` — new
3. `configs/paper.deepseek.yaml` — new
4. `internal/scheduler/scheduler_e2e_test.go` — new
5. `OPENCLAW_PROMPT.md` — new
6. `deploy/scripts/monitor.sh` — new (improved)
7. `internal/app/config.go` — added API key validation
8. `internal/app/config_test.go` — added TestMain with env vars
9. `internal/llm/client.go` — added DeepSeek support, fallback logic
10. `internal/llm/client_test.go` — added DeepSeek tests, live smoke test
11. `internal/scheduler/scheduler.go` — added LLM cap enforcement
12. `internal/scheduler/scheduler_test.go` — added cap tests
13. `internal/db/repositories.go` — added new repo methods
14. `internal/db/postgres_impl.go` — implemented new repo methods
15. `internal/db/postgres_impl_test.go` — added TAKE_PROFIT_MARKET test
16. `internal/domain/domain.go` — added CandidateID to LLMDecision
17. `.github/workflows/deploy-paper.yml` — switched to paper.deepseek.yaml
18. `deploy/systemd/botsurv-paper.service` — switched to paper.deepseek.yaml

---

## Cara Deploy Manual (Kalo Ga Bisa Git Pull)

Karena VPS ga punya akses GitHub, deploy manual dengan copy file:

```bash
# 1. Build binary di local
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o bot ./cmd/bot

# 2. Copy ke VPS
scp bot root@178.105.27.231:/opt/botsurv/app/bot
scp configs/paper.deepseek.yaml root@178.105.27.231:/opt/botsurv/app/configs/
scp -r migrations/ root@178.105.27.231:/opt/botsurv/app/

# 3. Restart service
ssh root@178.105.27.231 "systemctl restart botsurv-paper"
```

Atau taruh semua di tar.gz terus upload:
```bash
tar -czf botsurv-deploy.tar.gz bot configs/ migrations/ deploy/
scp botsurv-deploy.tar.gz root@178.105.27.231:/tmp/
ssh root@178.105.27.231 "cd /opt/botsurv/app && tar -xzf /tmp/botsurv-deploy.tar.gz && systemctl restart botsurv-paper"
```

---

## Status Saat Ini (VPS)

- **Service**: `botsurv-paper` — active, running `paper.deepseek.yaml`
- **DB**: PostgreSQL, 53MB, 180K+ candles
- **Config**: `paper.deepseek.yaml` (all coins, DeepSeek LLM)
- **Env**: `/etc/botsurv/paper.env` (DEEPSEEK_API_KEY, DATABASE_DSN)
- **Monitor**: `/opt/botsurv/monitor.sh` (improved)
