#!/usr/bin/env bash
# BotSurv operational monitor. Safe read-only checks for paper mode.
set -euo pipefail

APP_DIR="/opt/botsurv/app"
# Auto-detect running config from process
RUNNING_CONFIG=$(ps aux | grep '/bot run --config' | grep -v grep | sed 's/.*--config //' | awk '{print $1}' || true)
CONFIG="${RUNNING_CONFIG:-$APP_DIR/configs/paper.deepseek.yaml}"
ENV_FILE="/etc/botsurv/paper.env"
DB="botsurv"
NOW_UTC="$(date -u '+%Y-%m-%d %H:%M:%S UTC')"
NOW_WIB="$(TZ='Asia/Jakarta' date '+%Y-%m-%d %H:%M:%S WIB')"
WARNINGS=()

if [ -f "$ENV_FILE" ]; then
  set -a
  # shellcheck disable=SC1090
  . "$ENV_FILE"
  set +a
fi

psql_one() {
  sudo -u postgres psql -d "$DB" -X -q -t -A -c "$1" 2>/dev/null | head -1 | xargs || true
}

psql_rows() {
  sudo -u postgres psql -d "$DB" -X -q -t -A -F ' | ' -c "$1" 2>/dev/null || true
}

table_exists() {
  sudo -u postgres psql -d "$DB" -X -q -t -A -c "SELECT EXISTS (SELECT FROM pg_tables WHERE schemaname='public' AND tablename='$1');" 2>/dev/null | head -1 | xargs || echo false
}

add_warn() {
  WARNINGS+=("$1")
}

# --- Service Checks ---
service_status=$(systemctl is-active botsurv-paper.service 2>/dev/null || echo unknown)
service_enabled=$(systemctl is-enabled botsurv-paper.service 2>/dev/null || echo unknown)
if [ "$service_status" != "active" ]; then
  add_warn "SERVICE_NOT_ACTIVE=$service_status"
fi
if [ "$service_enabled" != "enabled" ]; then
  add_warn "SERVICE_NOT_ENABLED=$service_enabled"
fi

restarts=$(systemctl show botsurv-paper.service -p NRestarts --value 2>/dev/null || echo 0)
main_pid=$(systemctl show botsurv-paper.service -p MainPID --value 2>/dev/null || echo 0)
mem_current=$(systemctl show botsurv-paper.service -p MemoryCurrent --value 2>/dev/null || echo 0)
mem_mb=$((mem_current / 1024 / 1024))

# --- Disk Space Check ---
disk_usage_pct=$(df / | awk 'NR==2 {print $5}' | tr -d '%')
if [ "$disk_usage_pct" -gt 85 ]; then
  add_warn "DISK_HIGH=${disk_usage_pct}%"
fi

# --- DB Health ---
pg_isready=$(pg_isready -d "$DB" 2>/dev/null | grep -c "accepting connections" || echo 0)
if [ "$pg_isready" = "0" ]; then
  add_warn "POSTGRES_NOT_READY"
fi

# --- Core DB Counts ---
cycles=$(psql_one "select count(*) from cycles;")
candidates=$(psql_one "select count(*) from candidates;")
candles=$(psql_one "select count(*) from candles;")
open_positions=$(psql_one "select count(*) from positions where status='open';")
open_orders=$(psql_one "select count(*) from orders where status in ('pending','partially_filled');")
closed_24h=$(psql_one "select count(*) from positions where status='closed' and closed_at >= now() - interval '24 hours';")
paper_trades_24h=$(psql_one "select count(*) from paper_trades where closed_at >= now() - interval '24 hours' or (closed_at is null and opened_at >= now() - interval '24 hours');")
paper_pnl_24h=$(psql_one "select coalesce(sum(pnl_net),0) from paper_trades where closed_at >= now() - interval '24 hours';")
snapshots=$(psql_one "select count(*) from account_snapshots;")

# LLM stats
llm_decisions=$(psql_one "select count(*) from llm_decisions;")
llm_breakdown=$(psql_one "select string_agg(t.row, ', ') from (select decision||':'||count(*)::text as row from llm_decisions where created_at >= now() - interval '24 hours' group by decision order by count(*) desc) t;")
if [ "$(table_exists 'public.llm_usage_daily')" = "true" ]; then
  llm_calls_today=$(psql_one "select coalesce(sum(calls),0) from llm_usage_daily where usage_date = current_date;")
  llm_cost_today=$(psql_one "select coalesce(sum(cost_usd),0) from llm_usage_daily where usage_date = current_date;")
else
  llm_calls_today=$(psql_one "select count(*) from llm_decisions where created_at >= current_date;")
  llm_cost_today="?"
fi

# --- Safety Checks ---
unsafe_positions=$(psql_one "select count(*) from positions where status='open' and stop_loss <= 0;")
stale_pending=$(psql_one "select count(*) from orders where status in ('pending','partially_filled') and created_at < now() - interval '2 hours';")

if [ "${unsafe_positions:-0}" != "0" ]; then
  add_warn "OPEN_POSITION_WITHOUT_SL=$unsafe_positions"
fi
if [ "${stale_pending:-0}" != "0" ]; then
  add_warn "PENDING_ORDER_OLDER_THAN_2H=$stale_pending"
fi

# --- Freshness Checks ---
latest_cycle=$(psql_one "select coalesce(max(started_at)::text,'') from cycles;")
latest_candle_ms=$(psql_one "select coalesce(max(open_time)::text,'') from candles;")
latest_cycle_age=$(psql_one "select coalesce(floor(extract(epoch from now() - max(started_at)))::text,'') from cycles;")
latest_candle_age=$(psql_one "select coalesce(floor(extract(epoch from now()) - max(open_time)/1000.0)::text,'') from candles;")
latest_cycle_id=$(psql_one "select coalesce(cycle_id,'') from cycles order by started_at desc limit 1;")
latest_cycle_status=$(psql_one "select coalesce(status,'') from cycles order by started_at desc limit 1;")

# Thresholds for 15m scheduler/candles
if [ -n "$latest_cycle_age" ] && [ "$latest_cycle_age" -gt 1200 ]; then
  add_warn "NO_NEW_CYCLE_${latest_cycle_age}s"
fi
if [ -n "$latest_candle_age" ] && [ "$latest_candle_age" -gt 1800 ]; then
  add_warn "NO_FRESH_CANDLE_${latest_candle_age}s"
fi

# --- Journal Checks ---
journal_bad=$(journalctl -u botsurv-paper.service --since '30 minutes ago' --no-pager 2>/dev/null | grep -Eic 'panic|fatal|segmentation|data race' || true)
if [ "${journal_bad:-0}" != "0" ]; then
  add_warn "JOURNAL_FATAL_OR_PANIC=$journal_bad"
fi

orderbook_spam=$(journalctl -u botsurv-paper.service --since '10 minutes ago' --no-pager 2>/dev/null | grep -ci 'orderbook delta sequence gap' || true)
if [ "${orderbook_spam:-0}" -gt 100 ]; then
  add_warn "ORDERBOOK_WARNING_SPAM=$orderbook_spam"
fi

# --- WS Health Check ---
ws_disconnects=$(journalctl -u botsurv-paper.service --since '30 minutes ago' --no-pager 2>/dev/null | grep -ci 'websocket disconnected\|ws reconnect\|connection reset' || true)
if [ "${ws_disconnects:-0}" -gt 10 ]; then
  add_warn "WS_UNSTABLE=$ws_disconnects"
fi

# --- Data Retrieval ---
snapshot=$(psql_rows "select balance, equity, used_margin, realized_pnl, unrealized_pnl, total_fees, total_slippage, daily_loss, recorded_at from account_snapshots order by recorded_at desc limit 1;")
open_pos_rows=$(psql_rows "select symbol, side, entry_price, size, stop_loss, take_profit, unrealized_pnl from positions where status='open' order by opened_at desc limit 10;")
open_order_rows=$(psql_rows "select symbol, order_type, side, qty, coalesce(price,0), coalesce(stop_price,0), status, created_at from orders where status in ('pending','partially_filled') order by created_at desc limit 10;")

# --- CLI Status ---
cli_positions=""
cli_orders=""
cli_report=""
if [ -x "$APP_DIR/bot" ]; then
  cli_positions=$(timeout 20s "$APP_DIR/bot" positions --config "$CONFIG" 2>&1 || true)
  cli_orders=$(timeout 20s "$APP_DIR/bot" orders --config "$CONFIG" 2>&1 || true)
  cli_report=$(timeout 20s "$APP_DIR/bot" report --config "$CONFIG" 2>&1 || true)
fi

# --- Status Summary ---
warning_status="OK"
if [ "${#WARNINGS[@]}" -gt 0 ]; then
  warning_status="WARN: ${WARNINGS[*]}"
fi

cat <<REPORT
================ BotSurv Monitor ================
UTC: $NOW_UTC | WIB: $NOW_WIB
Config: $CONFIG
Service: $service_status | enabled=$service_enabled | pid=$main_pid | restarts=$restarts | mem=${mem_mb}MB
Disk usage: ${disk_usage_pct}% | Postgres ready: $pg_isready
Latest cycle: ${latest_cycle_id:-n/a} | ${latest_cycle_status:-n/a} | age=${latest_cycle_age:-n/a}s | at=${latest_cycle:-n/a}
Latest candle age: ${latest_candle_age:-n/a}s | latest_open_time_ms=${latest_candle_ms:-n/a}
DB counts: cycles=${cycles:-0}, candidates=${candidates:-0}, candles=${candles:-0}, snapshots=${snapshots:-0}
LLM: decisions=${llm_decisions:-0}, calls_today=${llm_calls_today:-0}, cost_today=\$${llm_cost_today:-0}
LLM breakdown (24h): ${llm_breakdown:-none}
Trading state: open_positions=${open_positions:-0}, open_orders=${open_orders:-0}, closed_24h=${closed_24h:-0}
Paper trades 24h: ${paper_trades_24h:-0} | PnL 24h: \$${paper_pnl_24h:-0}
Safety: unsafe_positions=${unsafe_positions:-0}, stale_pending=${stale_pending:-0}
Journal: fatal_30m=${journal_bad:-0}, orderbook_gap_10m=${orderbook_spam:-0}, ws_disconnects_30m=${ws_disconnects:-0}
Status: $warning_status

-- Latest account snapshot --
${snapshot:-none}

-- Open positions --
${open_pos_rows:-none}

-- Open orders --
${open_order_rows:-none}

-- CLI positions --
${cli_positions:-not available}

-- CLI orders --
${cli_orders:-not available}

-- CLI report --
${cli_report:-not available}
=================================================
REPORT
