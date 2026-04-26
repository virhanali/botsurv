package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/virhan/botsurv/internal/domain"
)

// NewPostgresRepositories creates a PostgreSQL-backed repository set.
func NewPostgresRepositories(db *sql.DB) *Repositories {
	return &Repositories{
		CandleRepository:    &postgresCandleRepository{db: db},
		UniverseRepository:  &postgresUniverseRepository{db: db},
		CycleRepository:     &postgresCycleRepository{db: db},
		CandidateRepository: &postgresCandidateRepository{db: db},
	}
}

func marshalStringSlice(values []string) (string, error) {
	if values == nil {
		values = []string{}
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("marshal string slice: %w", err)
	}
	return string(raw), nil
}

func unmarshalStringSlice(raw string) ([]string, error) {
	if raw == "" || raw == "[]" {
		return []string{}, nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, fmt.Errorf("unmarshal string slice: %w", err)
	}
	return values, nil
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// --- CandleRepository ---

type postgresCandleRepository struct {
	db *sql.DB
}

func (r *postgresCandleRepository) Insert(ctx context.Context, c domain.Candle) (int64, error) {
	var id int64
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO candles (symbol, timeframe, open_time, open, high, low, close, volume, turn_over, confirmed)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 ON CONFLICT(symbol, timeframe, open_time) DO UPDATE SET
		 open=excluded.open, high=excluded.high, low=excluded.low, close=excluded.close,
		 volume=excluded.volume, turn_over=excluded.turn_over, confirmed=excluded.confirmed
		 RETURNING id`,
		c.Symbol, c.Timeframe, c.OpenTime, c.Open, c.High, c.Low, c.Close, c.Volume, c.TurnOver, c.Confirmed).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert candle: %w", err)
	}
	return id, nil
}

func (r *postgresCandleRepository) GetBySymbolTimeframe(ctx context.Context, symbol, timeframe string, limit int) ([]domain.Candle, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT symbol, timeframe, open_time, open, high, low, close, volume, turn_over, confirmed
		 FROM (
			 SELECT * FROM candles
			 WHERE symbol = $1 AND timeframe = $2
			 ORDER BY open_time DESC
			 LIMIT $3
		 ) sub
		 ORDER BY open_time ASC`,
		symbol, timeframe, limit)
	if err != nil {
		return nil, fmt.Errorf("query candles: %w", err)
	}
	defer rows.Close()

	var candles []domain.Candle
	for rows.Next() {
		var c domain.Candle
		if err := rows.Scan(&c.Symbol, &c.Timeframe, &c.OpenTime, &c.Open, &c.High, &c.Low, &c.Close, &c.Volume, &c.TurnOver, &c.Confirmed); err != nil {
			return nil, fmt.Errorf("scan candle: %w", err)
		}
		candles = append(candles, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate candles: %w", err)
	}
	return candles, nil
}

// --- UniverseRepository ---

type postgresUniverseRepository struct {
	db *sql.DB
}

func (r *postgresUniverseRepository) InsertOrUpdate(ctx context.Context, s domain.UniverseSymbol) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO universe_symbols (symbol, status, quote_asset, base_asset, min_notional, tick_size, lot_size, max_leverage, blacklist, force_include, liquidity_score, last_scan_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		 ON CONFLICT(symbol) DO UPDATE SET
		 status=excluded.status, quote_asset=excluded.quote_asset, base_asset=excluded.base_asset,
		 min_notional=excluded.min_notional, tick_size=excluded.tick_size, lot_size=excluded.lot_size,
		 max_leverage=excluded.max_leverage, blacklist=excluded.blacklist, force_include=excluded.force_include,
		 liquidity_score=excluded.liquidity_score, last_scan_at=excluded.last_scan_at`,
		s.Symbol, s.Status, s.QuoteAsset, s.BaseAsset, s.MinNotional, s.TickSize, s.LotSize, s.MaxLeverage,
		s.Blacklist, s.ForceInclude, s.LiquidityScore, nullableTime(s.LastScanAt))
	if err != nil {
		return fmt.Errorf("insert universe symbol: %w", err)
	}
	return nil
}

func (r *postgresUniverseRepository) GetAll(ctx context.Context) ([]domain.UniverseSymbol, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT symbol, status, quote_asset, base_asset, min_notional, tick_size, lot_size, max_leverage, blacklist, force_include, liquidity_score, last_scan_at
		 FROM universe_symbols ORDER BY symbol`)
	if err != nil {
		return nil, fmt.Errorf("query universe symbols: %w", err)
	}
	defer rows.Close()

	var symbols []domain.UniverseSymbol
	for rows.Next() {
		var s domain.UniverseSymbol
		var lastScan sql.NullTime
		if err := rows.Scan(&s.Symbol, &s.Status, &s.QuoteAsset, &s.BaseAsset, &s.MinNotional, &s.TickSize, &s.LotSize, &s.MaxLeverage, &s.Blacklist, &s.ForceInclude, &s.LiquidityScore, &lastScan); err != nil {
			return nil, fmt.Errorf("scan universe symbol: %w", err)
		}
		if lastScan.Valid {
			s.LastScanAt = lastScan.Time
		}
		symbols = append(symbols, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate universe symbols: %w", err)
	}
	return symbols, nil
}

func (r *postgresUniverseRepository) GetBySymbol(ctx context.Context, symbol string) (*domain.UniverseSymbol, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT symbol, status, quote_asset, base_asset, min_notional, tick_size, lot_size, max_leverage, blacklist, force_include, liquidity_score, last_scan_at
		 FROM universe_symbols WHERE symbol = $1`, symbol)
	var s domain.UniverseSymbol
	var lastScan sql.NullTime
	err := row.Scan(&s.Symbol, &s.Status, &s.QuoteAsset, &s.BaseAsset, &s.MinNotional, &s.TickSize, &s.LotSize, &s.MaxLeverage, &s.Blacklist, &s.ForceInclude, &s.LiquidityScore, &lastScan)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get universe symbol: %w", err)
	}
	if lastScan.Valid {
		s.LastScanAt = lastScan.Time
	}
	return &s, nil
}

// --- CycleRepository ---

type postgresCycleRepository struct {
	db *sql.DB
}

func (r *postgresCycleRepository) Insert(ctx context.Context, c domain.Cycle) (int64, error) {
	reasonCodesJSON, err := marshalStringSlice(c.ReasonCodes)
	if err != nil {
		return 0, err
	}
	var id int64
	err = r.db.QueryRowContext(ctx,
		`INSERT INTO cycles (cycle_id, started_at, ended_at, status, reason_codes)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id`,
		c.CycleID, c.StartedAt, c.EndedAt, c.Status, reasonCodesJSON).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert cycle: %w", err)
	}
	return id, nil
}

func (r *postgresCycleRepository) GetLatest(ctx context.Context) (*domain.Cycle, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, cycle_id, started_at, ended_at, status, reason_codes
		 FROM cycles ORDER BY started_at DESC LIMIT 1`)
	var c domain.Cycle
	var endedAt sql.NullTime
	var reasonCodesJSON string
	err := row.Scan(&c.ID, &c.CycleID, &c.StartedAt, &endedAt, &c.Status, &reasonCodesJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get latest cycle: %w", err)
	}
	if endedAt.Valid {
		c.EndedAt = &endedAt.Time
	}
	c.ReasonCodes, err = unmarshalStringSlice(reasonCodesJSON)
	if err != nil {
		return nil, fmt.Errorf("decode cycle reason codes: %w", err)
	}
	return &c, nil
}

// --- CandidateRepository ---

type postgresCandidateRepository struct {
	db *sql.DB
}

func (r *postgresCandidateRepository) Insert(ctx context.Context, c domain.Candidate) (int64, error) {
	reasonCodesJSON, err := marshalStringSlice(c.ReasonCodes)
	if err != nil {
		return 0, err
	}
	routingReasonCodesJSON, err := marshalStringSlice(c.LLMRoutingReasonCodes)
	if err != nil {
		return 0, err
	}
	var id int64
	err = r.db.QueryRowContext(ctx,
		`INSERT INTO candidates (cycle_id, symbol, candidate_score, liquidity_score, execution_score, setup_score, volatility_score, llm_eligible, llm_routing_reason_codes, regime, setup_type, side, entry_type, proposed_entry, proposed_stop_loss, proposed_take_profit, stop_loss_pct, take_profit_pct, rr, invalidation_level, expected_move, estimated_total_cost, reason_codes)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23)
		 RETURNING id`,
		c.CycleID, c.Symbol, c.CandidateScore, c.LiquidityScore, c.ExecutionScore, c.SetupScore, c.VolatilityScore,
		c.LLMEligible, routingReasonCodesJSON, c.Regime, c.SetupType, c.Side, c.EntryType, c.ProposedEntry,
		c.ProposedStopLoss, c.ProposedTakeProfit, c.StopLossPct, c.TakeProfitPct, c.RR, c.InvalidationLevel, c.ExpectedMove, c.EstimatedTotalCost, reasonCodesJSON).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert candidate: %w", err)
	}
	return id, nil
}

func (r *postgresCandidateRepository) GetByCycle(ctx context.Context, cycleID string) ([]domain.Candidate, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT cycle_id, symbol, candidate_score, liquidity_score, execution_score, setup_score, volatility_score, llm_eligible, llm_routing_reason_codes, regime, setup_type, side, entry_type, proposed_entry, proposed_stop_loss, proposed_take_profit, stop_loss_pct, take_profit_pct, rr, invalidation_level, expected_move, estimated_total_cost, reason_codes
		 FROM candidates WHERE cycle_id = $1 ORDER BY id`,
		cycleID)
	if err != nil {
		return nil, fmt.Errorf("query candidates: %w", err)
	}
	defer rows.Close()

	var candidates []domain.Candidate
	for rows.Next() {
		var c domain.Candidate
		var routingReasonCodesJSON string
		var reasonCodesJSON string
		if err := rows.Scan(&c.CycleID, &c.Symbol, &c.CandidateScore, &c.LiquidityScore, &c.ExecutionScore, &c.SetupScore, &c.VolatilityScore,
			&c.LLMEligible, &routingReasonCodesJSON, &c.Regime, &c.SetupType, &c.Side, &c.EntryType, &c.ProposedEntry,
			&c.ProposedStopLoss, &c.ProposedTakeProfit, &c.StopLossPct, &c.TakeProfitPct, &c.RR, &c.InvalidationLevel, &c.ExpectedMove, &c.EstimatedTotalCost, &reasonCodesJSON); err != nil {
			return nil, fmt.Errorf("scan candidate: %w", err)
		}
		var err error
		c.LLMRoutingReasonCodes, err = unmarshalStringSlice(routingReasonCodesJSON)
		if err != nil {
			return nil, fmt.Errorf("decode candidate routing reason codes: %w", err)
		}
		c.ReasonCodes, err = unmarshalStringSlice(reasonCodesJSON)
		if err != nil {
			return nil, fmt.Errorf("decode candidate reason codes: %w", err)
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate candidates: %w", err)
	}
	return candidates, nil
}
