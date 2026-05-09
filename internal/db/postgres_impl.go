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
		CandleRepository:             &postgresCandleRepository{db: db},
		UniverseRepository:           &postgresUniverseRepository{db: db},
		CycleRepository:              &postgresCycleRepository{db: db},
		CandidateRepository:          &postgresCandidateRepository{db: db},
		PositionRepository:           &postgresPositionRepository{db: db},
		OrderRepository:              &postgresOrderRepository{db: db},
		ExecutionRepository:          &postgresExecutionRepository{db: db},
		AccountSnapshotRepository:    &postgresAccountSnapshotRepository{db: db},
		LLMDecisionRepository:        &postgresLLMDecisionRepository{db: db},
		RiskDecisionRepository:       &postgresRiskDecisionRepository{db: db},
		LLMUsageRepository:           &postgresLLMUsageRepository{db: db},
		DecisionLogRepository:        &postgresDecisionLogRepository{db: db},
		CandidateOutcomeRepository:   &postgresCandidateOutcomeRepository{db: db},
		PaperTradeRepository:         &postgresPaperTradeRepository{db: db},
		PaperAccountStateRepository:  &postgresPaperAccountStateRepository{db: db},
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

func nullableFloat64Ptr(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullableInt64Ptr(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
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

func (r *postgresCycleRepository) Update(ctx context.Context, c domain.Cycle) error {
	reasonCodesJSON, err := marshalStringSlice(c.ReasonCodes)
	if err != nil {
		return err
	}
	var endedAt any
	if c.EndedAt != nil {
		endedAt = *c.EndedAt
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE cycles SET ended_at=$1, status=$2, reason_codes=$3 WHERE cycle_id=$4`,
		endedAt, c.Status, reasonCodesJSON, c.CycleID)
	if err != nil {
		return fmt.Errorf("update cycle: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("cycle not found: %s", c.CycleID)
	}
	return nil
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

// --- PositionRepository ---

type postgresPositionRepository struct {
	db *sql.DB
}

func (r *postgresPositionRepository) Insert(ctx context.Context, p domain.Position) (int64, error) {
	var id int64
	var tp *float64
	if p.TakeProfit > 0 {
		tp = &p.TakeProfit
	}
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO positions (symbol, side, entry_price, size, leverage, margin, stop_loss, take_profit, unrealized_pnl, realized_pnl, status, source, opened_at, sl_order_id, tp_order_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		 RETURNING id`,
		p.Symbol, string(p.Side), p.EntryPrice, p.Size, p.Leverage, p.Margin, p.StopLoss, tp,
		p.UnrealizedPnL, p.RealizedPnL, string(p.Status), p.Source, p.OpenedAt,
		nullableInt64Ptr(p.SLOrderID), nullableInt64Ptr(p.TPOrderID)).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert position: %w", err)
	}
	return id, nil
}

func (r *postgresPositionRepository) Update(ctx context.Context, p domain.Position) error {
	var closedAt any
	if p.ClosedAt != nil {
		closedAt = *p.ClosedAt
	}
	var tp any
	if p.TakeProfit > 0 {
		tp = p.TakeProfit
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE positions SET entry_price=$1, size=$2, leverage=$3, margin=$4, stop_loss=$5, take_profit=$6,
		 unrealized_pnl=$7, realized_pnl=$8, status=$9, source=$10, closed_at=$11,
		 sl_order_id=$12, tp_order_id=$13
		 WHERE id=$14`,
		p.EntryPrice, p.Size, p.Leverage, p.Margin, p.StopLoss, tp,
		p.UnrealizedPnL, p.RealizedPnL, string(p.Status), p.Source, closedAt,
		nullableInt64Ptr(p.SLOrderID), nullableInt64Ptr(p.TPOrderID), p.ID)
	if err != nil {
		return fmt.Errorf("update position: %w", err)
	}
	return nil
}

func (r *postgresPositionRepository) GetOpen(ctx context.Context) ([]domain.Position, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, symbol, side, entry_price, size, leverage, margin, stop_loss, take_profit,
		 unrealized_pnl, realized_pnl, status, source, opened_at, closed_at,
		 sl_order_id, tp_order_id
		 FROM positions WHERE status = 'open' ORDER BY opened_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("query open positions: %w", err)
	}
	defer rows.Close()
	return scanPositions(rows)
}

func (r *postgresPositionRepository) GetClosed(ctx context.Context, since time.Time) ([]domain.Position, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, symbol, side, entry_price, size, leverage, margin, stop_loss, take_profit,
		 unrealized_pnl, realized_pnl, status, source, opened_at, closed_at,
		 sl_order_id, tp_order_id
		 FROM positions WHERE status = 'closed' AND closed_at >= $1 ORDER BY closed_at DESC`, since)
	if err != nil {
		return nil, fmt.Errorf("query closed positions: %w", err)
	}
	defer rows.Close()
	return scanPositions(rows)
}

func (r *postgresPositionRepository) GetBySymbol(ctx context.Context, symbol string) (*domain.Position, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, symbol, side, entry_price, size, leverage, margin, stop_loss, take_profit,
		 unrealized_pnl, realized_pnl, status, source, opened_at, closed_at,
		 sl_order_id, tp_order_id
		 FROM positions WHERE symbol = $1 AND status = 'open'`, symbol)
	var p domain.Position
	var side, status, source string
	var tp sql.NullFloat64
	var closedAt sql.NullTime
	var slOrderID, tpOrderID sql.NullInt64
	err := row.Scan(&p.ID, &p.Symbol, &side, &p.EntryPrice, &p.Size, &p.Leverage, &p.Margin,
		&p.StopLoss, &tp, &p.UnrealizedPnL, &p.RealizedPnL, &status, &source, &p.OpenedAt, &closedAt,
		&slOrderID, &tpOrderID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get position by symbol: %w", err)
	}
	p.Side = domain.Side(side)
	p.Status = domain.PositionStatus(status)
	p.Source = source
	if tp.Valid {
		p.TakeProfit = tp.Float64
	}
	if closedAt.Valid {
		p.ClosedAt = &closedAt.Time
	}
	if slOrderID.Valid {
		p.SLOrderID = &slOrderID.Int64
	}
	if tpOrderID.Valid {
		p.TPOrderID = &tpOrderID.Int64
	}
	return &p, nil
}

func scanPositions(rows *sql.Rows) ([]domain.Position, error) {
	var positions []domain.Position
	for rows.Next() {
		var p domain.Position
		var side, status, source string
		var tp sql.NullFloat64
		var closedAt sql.NullTime
		var slOrderID, tpOrderID sql.NullInt64
		if err := rows.Scan(&p.ID, &p.Symbol, &side, &p.EntryPrice, &p.Size, &p.Leverage, &p.Margin,
			&p.StopLoss, &tp, &p.UnrealizedPnL, &p.RealizedPnL, &status, &source, &p.OpenedAt, &closedAt,
			&slOrderID, &tpOrderID); err != nil {
			return nil, fmt.Errorf("scan position: %w", err)
		}
		p.Side = domain.Side(side)
		p.Status = domain.PositionStatus(status)
		p.Source = source
		if tp.Valid {
			p.TakeProfit = tp.Float64
		}
		if closedAt.Valid {
			p.ClosedAt = &closedAt.Time
		}
		if slOrderID.Valid {
			p.SLOrderID = &slOrderID.Int64
		}
		if tpOrderID.Valid {
			p.TPOrderID = &tpOrderID.Int64
		}
		positions = append(positions, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate positions: %w", err)
	}
	return positions, nil
}

// --- OrderRepository ---

type postgresOrderRepository struct {
	db *sql.DB
}

func (r *postgresOrderRepository) Insert(ctx context.Context, o domain.Order) (int64, error) {
	var id int64
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO orders (broker_order_id, position_id, symbol, side, order_type, qty, price, stop_price, status, created_at, updated_at, intended_stop_loss, intended_take_profit)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		 RETURNING id`,
		o.BrokerOrderID, nullableInt64Ptr(o.PositionID), o.Symbol, string(o.Side), string(o.OrderType),
		o.Qty, nullableFloat64Ptr(o.Price), nullableFloat64Ptr(o.StopPrice),
		string(o.Status), o.CreatedAt, o.UpdatedAt, o.IntendedSL, o.IntendedTP).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert order: %w", err)
	}
	return id, nil
}

func (r *postgresOrderRepository) Update(ctx context.Context, o domain.Order) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE orders SET broker_order_id=$1, position_id=$2, symbol=$3, side=$4, order_type=$5,
		 qty=$6, price=$7, stop_price=$8, status=$9, updated_at=$10,
		 intended_stop_loss=$11, intended_take_profit=$12
		 WHERE id=$13`,
		o.BrokerOrderID, nullableInt64Ptr(o.PositionID), o.Symbol, string(o.Side), string(o.OrderType),
		o.Qty, nullableFloat64Ptr(o.Price), nullableFloat64Ptr(o.StopPrice),
		string(o.Status), o.UpdatedAt, o.IntendedSL, o.IntendedTP, o.ID)
	if err != nil {
		return fmt.Errorf("update order: %w", err)
	}
	return nil
}

func (r *postgresOrderRepository) GetOpen(ctx context.Context) ([]domain.Order, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, broker_order_id, position_id, symbol, side, order_type, qty, price, stop_price, status, created_at, updated_at, intended_stop_loss, intended_take_profit
		 FROM orders WHERE status IN ('pending', 'partially_filled') ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("query open orders: %w", err)
	}
	defer rows.Close()
	return scanOrders(rows)
}

func (r *postgresOrderRepository) GetBySymbol(ctx context.Context, symbol string) ([]domain.Order, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, broker_order_id, position_id, symbol, side, order_type, qty, price, stop_price, status, created_at, updated_at, intended_stop_loss, intended_take_profit
		 FROM orders WHERE symbol = $1 AND status IN ('pending', 'partially_filled') ORDER BY created_at DESC`, symbol)
	if err != nil {
		return nil, fmt.Errorf("query orders by symbol: %w", err)
	}
	defer rows.Close()
	return scanOrders(rows)
}

func (r *postgresOrderRepository) GetAll(ctx context.Context, since time.Time) ([]domain.Order, error) {
	query := `SELECT id, broker_order_id, position_id, symbol, side, order_type, qty, price, stop_price, status, created_at, updated_at, intended_stop_loss, intended_take_profit
		 FROM orders WHERE created_at >= $1 OR updated_at >= $1 ORDER BY created_at DESC`
	rows, err := r.db.QueryContext(ctx, query, since)
	if err != nil {
		return nil, fmt.Errorf("query all orders: %w", err)
	}
	defer rows.Close()
	return scanOrders(rows)
}

func scanOrders(rows *sql.Rows) ([]domain.Order, error) {
	var orders []domain.Order
	for rows.Next() {
		var o domain.Order
		var side, orderType, status string
		var positionID sql.NullInt64
		var price, stopPrice sql.NullFloat64
		if err := rows.Scan(&o.ID, &o.BrokerOrderID, &positionID, &o.Symbol, &side, &orderType,
			&o.Qty, &price, &stopPrice, &status, &o.CreatedAt, &o.UpdatedAt, &o.IntendedSL, &o.IntendedTP); err != nil {
			return nil, fmt.Errorf("scan order: %w", err)
		}
		o.Side = domain.OrderSide(side)
		o.OrderType = domain.OrderType(orderType)
		o.Status = domain.OrderStatus(status)
		if positionID.Valid {
			o.PositionID = &positionID.Int64
		}
		if price.Valid {
			o.Price = &price.Float64
		}
		if stopPrice.Valid {
			o.StopPrice = &stopPrice.Float64
		}
		orders = append(orders, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate orders: %w", err)
	}
	return orders, nil
}

// --- ExecutionRepository ---

type postgresExecutionRepository struct {
	db *sql.DB
}

func (r *postgresExecutionRepository) Insert(ctx context.Context, e domain.Execution) (int64, error) {
	var id int64
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO executions (order_id, symbol, side, qty, price, fee, slippage, executed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING id`,
		e.OrderID, e.Symbol, string(e.Side), e.Qty, e.Price, e.Fee, e.Slippage, e.ExecutedAt).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert execution: %w", err)
	}
	return id, nil
}

func (r *postgresExecutionRepository) GetByOrder(ctx context.Context, orderID int64) ([]domain.Execution, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, order_id, symbol, side, qty, price, fee, slippage, executed_at
		 FROM executions WHERE order_id = $1 ORDER BY executed_at`, orderID)
	if err != nil {
		return nil, fmt.Errorf("query executions: %w", err)
	}
	defer rows.Close()

	var executions []domain.Execution
	for rows.Next() {
		var e domain.Execution
		var side string
		if err := rows.Scan(&e.ID, &e.OrderID, &e.Symbol, &side, &e.Qty, &e.Price, &e.Fee, &e.Slippage, &e.ExecutedAt); err != nil {
			return nil, fmt.Errorf("scan execution: %w", err)
		}
		e.Side = domain.OrderSide(side)
		executions = append(executions, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate executions: %w", err)
	}
	return executions, nil
}

// --- AccountSnapshotRepository ---

type postgresAccountSnapshotRepository struct {
	db *sql.DB
}

func (r *postgresAccountSnapshotRepository) Insert(ctx context.Context, a domain.AccountState) (int64, error) {
	var id int64
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO account_snapshots (balance, available_balance, used_margin, equity, realized_pnl, unrealized_pnl, total_fees, total_slippage, daily_loss, recorded_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 RETURNING id`,
		a.Balance, a.AvailableBalance, a.UsedMargin, a.Equity, a.RealizedPnL, a.UnrealizedPnL,
		a.TotalFees, a.TotalSlippage, a.DailyLoss, a.RecordedAt).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert account snapshot: %w", err)
	}
	return id, nil
}

func (r *postgresAccountSnapshotRepository) GetLatest(ctx context.Context) (*domain.AccountState, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, balance, available_balance, used_margin, equity, realized_pnl, unrealized_pnl, total_fees, total_slippage, daily_loss, recorded_at
		 FROM account_snapshots ORDER BY recorded_at DESC LIMIT 1`)
	var a domain.AccountState
	err := row.Scan(&a.ID, &a.Balance, &a.AvailableBalance, &a.UsedMargin, &a.Equity,
		&a.RealizedPnL, &a.UnrealizedPnL, &a.TotalFees, &a.TotalSlippage, &a.DailyLoss, &a.RecordedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get latest account snapshot: %w", err)
	}
	return &a, nil
}

func (r *postgresAccountSnapshotRepository) GetLatestBefore(ctx context.Context, before time.Time) (*domain.AccountState, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, balance, available_balance, used_margin, equity, realized_pnl, unrealized_pnl, total_fees, total_slippage, daily_loss, recorded_at
		 FROM account_snapshots WHERE recorded_at < $1 ORDER BY recorded_at DESC LIMIT 1`, before)
	var a domain.AccountState
	err := row.Scan(&a.ID, &a.Balance, &a.AvailableBalance, &a.UsedMargin, &a.Equity,
		&a.RealizedPnL, &a.UnrealizedPnL, &a.TotalFees, &a.TotalSlippage, &a.DailyLoss, &a.RecordedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get latest account snapshot: %w", err)
	}
	return &a, nil
}

// --- LLMDecisionRepository ---

type postgresLLMDecisionRepository struct {
	db *sql.DB
}

func (r *postgresLLMDecisionRepository) Insert(ctx context.Context, d domain.LLMDecision, candidateID int64, cycleID string) (int64, error) {
	reasonCodesJSON, err := marshalStringSlice(d.ReasonCodes)
	if err != nil {
		return 0, err
	}
	riskFlagsJSON, err := marshalStringSlice(d.RiskFlags)
	if err != nil {
		return 0, err
	}
	var candidateIDVal any
	if candidateID > 0 {
		candidateIDVal = candidateID
	}
	var id int64
	err = r.db.QueryRowContext(ctx,
		`INSERT INTO llm_decisions (candidate_id, cycle_id, raw_response, decision, confidence, size_multiplier, regime, reason_codes, risk_flags, notes, validation_status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		 RETURNING id`,
		candidateIDVal, cycleID, d.RawResponse, d.Decision, d.Confidence, d.SizeMultiplier,
		d.Regime, reasonCodesJSON, riskFlagsJSON, d.Notes, d.ValidationStatus).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert llm decision: %w", err)
	}
	return id, nil
}

func (r *postgresLLMDecisionRepository) GetByCycle(ctx context.Context, cycleID string) ([]domain.LLMDecision, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT candidate_id, raw_response, decision, confidence, size_multiplier, regime, reason_codes, risk_flags, notes, validation_status
		 FROM llm_decisions WHERE cycle_id = $1 ORDER BY created_at`, cycleID)
	if err != nil {
		return nil, fmt.Errorf("query llm decisions: %w", err)
	}
	defer rows.Close()

	var decisions []domain.LLMDecision
	for rows.Next() {
		var d domain.LLMDecision
		var reasonCodesJSON, riskFlagsJSON string
		var candidateID sql.NullInt64
		if err := rows.Scan(&candidateID, &d.RawResponse, &d.Decision, &d.Confidence, &d.SizeMultiplier,
			&d.Regime, &reasonCodesJSON, &riskFlagsJSON, &d.Notes, &d.ValidationStatus); err != nil {
			return nil, fmt.Errorf("scan llm decision: %w", err)
		}
		if candidateID.Valid {
			d.CandidateID = candidateID.Int64
		}
		d.ReasonCodes, err = unmarshalStringSlice(reasonCodesJSON)
		if err != nil {
			return nil, fmt.Errorf("decode reason codes: %w", err)
		}
		d.RiskFlags, err = unmarshalStringSlice(riskFlagsJSON)
		if err != nil {
			return nil, fmt.Errorf("decode risk flags: %w", err)
		}
		decisions = append(decisions, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate llm decisions: %w", err)
	}
	return decisions, nil
}

// --- RiskDecisionRepository ---

type postgresRiskDecisionRepository struct {
	db *sql.DB
}

func (r *postgresRiskDecisionRepository) Insert(ctx context.Context, d domain.RiskDecision, candidateID int64, cycleID string) (int64, error) {
	reasonCodesJSON, err := marshalStringSlice(d.ReasonCodes)
	if err != nil {
		return 0, err
	}
	var candidateIDVal any
	if candidateID > 0 {
		candidateIDVal = candidateID
	}
	var id int64
	err = r.db.QueryRowContext(ctx,
		`INSERT INTO risk_decisions (candidate_id, cycle_id, approved, final_position_notional, required_margin, estimated_loss, reason_codes, portfolio_rank, portfolio_reject_reason)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 RETURNING id`,
		candidateIDVal, cycleID, d.Approved, d.FinalPositionNotional, d.RequiredMargin, d.EstimatedLoss,
		reasonCodesJSON, d.PortfolioRank, d.PortfolioRejectReason).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert risk decision: %w", err)
	}
	return id, nil
}

func (r *postgresRiskDecisionRepository) GetByCycle(ctx context.Context, cycleID string) ([]domain.RiskDecision, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, candidate_id, cycle_id, approved, final_position_notional, required_margin, estimated_loss, reason_codes, portfolio_rank, portfolio_reject_reason, created_at
		 FROM risk_decisions WHERE cycle_id = $1 ORDER BY created_at, id`, cycleID)
	if err != nil {
		return nil, fmt.Errorf("query risk decisions: %w", err)
	}
	defer rows.Close()

	var decisions []domain.RiskDecision
	for rows.Next() {
		var d domain.RiskDecision
		var candidateID sql.NullInt64
		var reasonCodesJSON string
		if err := rows.Scan(&d.ID, &candidateID, &d.CycleID, &d.Approved, &d.FinalPositionNotional,
			&d.RequiredMargin, &d.EstimatedLoss, &reasonCodesJSON, &d.PortfolioRank,
			&d.PortfolioRejectReason, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan risk decision: %w", err)
		}
		if candidateID.Valid {
			d.CandidateID = candidateID.Int64
		}
		d.ReasonCodes, err = unmarshalStringSlice(reasonCodesJSON)
		if err != nil {
			return nil, fmt.Errorf("decode risk reason codes: %w", err)
		}
		decisions = append(decisions, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate risk decisions: %w", err)
	}
	return decisions, nil
}

// --- LLMUsageRepository ---

type postgresLLMUsageRepository struct {
	db *sql.DB
}

func (r *postgresLLMUsageRepository) Get(ctx context.Context, usageDate time.Time) (*domain.LLMUsageState, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT usage_date, calls, cost_usd, updated_at FROM llm_usage_daily WHERE usage_date = $1`,
		usageDate.Format("2006-01-02"))
	var state domain.LLMUsageState
	err := row.Scan(&state.UsageDate, &state.Calls, &state.CostUSD, &state.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get llm usage: %w", err)
	}
	return &state, nil
}

func (r *postgresLLMUsageRepository) IncrementCalls(ctx context.Context, usageDate time.Time, calls int, cost float64) error {
	if calls <= 0 {
		return nil
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO llm_usage_daily (usage_date, calls, cost_usd, updated_at)
		 VALUES ($1, $2, $3, NOW())
		 ON CONFLICT (usage_date) DO UPDATE SET
		 calls = llm_usage_daily.calls + EXCLUDED.calls,
		 cost_usd = EXCLUDED.cost_usd,
		 updated_at = NOW()`,
		usageDate.Format("2006-01-02"), calls, cost)
	if err != nil {
		return fmt.Errorf("increment llm usage: %w", err)
	}
	return nil
}

func (r *postgresExecutionRepository) GetAll(ctx context.Context, since time.Time) ([]domain.Execution, error) {
	query := `SELECT id, order_id, symbol, side, qty, price, fee, slippage, executed_at
		 FROM executions WHERE executed_at >= $1 ORDER BY executed_at DESC`
	if since.IsZero() {
		query = `SELECT id, order_id, symbol, side, qty, price, fee, slippage, executed_at
			 FROM executions ORDER BY executed_at DESC`
		rows, err := r.db.QueryContext(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("query executions: %w", err)
		}
		defer rows.Close()
		return scanExecutions(rows)
	}
	rows, err := r.db.QueryContext(ctx, query, since)
	if err != nil {
		return nil, fmt.Errorf("query executions: %w", err)
	}
	defer rows.Close()
	return scanExecutions(rows)
}

func scanExecutions(rows *sql.Rows) ([]domain.Execution, error) {
	var executions []domain.Execution
	for rows.Next() {
		var e domain.Execution
		var side string
		if err := rows.Scan(&e.ID, &e.OrderID, &e.Symbol, &side, &e.Qty, &e.Price, &e.Fee, &e.Slippage, &e.ExecutedAt); err != nil {
			return nil, fmt.Errorf("scan execution: %w", err)
		}
		e.Side = domain.OrderSide(side)
		executions = append(executions, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate executions: %w", err)
	}
	return executions, nil
}

func (r *postgresAccountSnapshotRepository) GetAll(ctx context.Context, since time.Time) ([]domain.AccountState, error) {
	query := `SELECT id, balance, available_balance, used_margin, equity, realized_pnl, unrealized_pnl, total_fees, total_slippage, daily_loss, recorded_at
		 FROM account_snapshots WHERE recorded_at >= $1 ORDER BY recorded_at DESC`
	if since.IsZero() {
		query = `SELECT id, balance, available_balance, used_margin, equity, realized_pnl, unrealized_pnl, total_fees, total_slippage, daily_loss, recorded_at
			 FROM account_snapshots ORDER BY recorded_at DESC`
		rows, err := r.db.QueryContext(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("query account snapshots: %w", err)
		}
		defer rows.Close()
		return scanAccountSnapshots(rows)
	}
	rows, err := r.db.QueryContext(ctx, query, since)
	if err != nil {
		return nil, fmt.Errorf("query account snapshots: %w", err)
	}
	defer rows.Close()
	return scanAccountSnapshots(rows)
}

func scanAccountSnapshots(rows *sql.Rows) ([]domain.AccountState, error) {
	var snapshots []domain.AccountState
	for rows.Next() {
		var a domain.AccountState
		if err := rows.Scan(&a.ID, &a.Balance, &a.AvailableBalance, &a.UsedMargin, &a.Equity,
			&a.RealizedPnL, &a.UnrealizedPnL, &a.TotalFees, &a.TotalSlippage, &a.DailyLoss, &a.RecordedAt); err != nil {
			return nil, fmt.Errorf("scan account snapshot: %w", err)
		}
		snapshots = append(snapshots, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate account snapshots: %w", err)
	}
	return snapshots, nil
}

// --- DecisionLogRepository ---

type postgresDecisionLogRepository struct {
	db *sql.DB
}

func (r *postgresDecisionLogRepository) Insert(ctx context.Context, d domain.DecisionLog) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO decision_logs (
			decision_id, cycle_id, timestamp, mode, symbol, timeframe,
			candle_count, data_validation_result,
			indicator_snapshot, regime_snapshot, strategy_attempted,
			candidate, score_breakdown, near_misses,
			risk_validation, safety_validation, order_plan,
			llm_review, llm_mode_active,
			final_action, final_action_reason,
			engine_version, scoring_version, risk_config_version, llm_prompt_version
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25)`,
		d.DecisionID, d.CycleID, d.Timestamp, d.Mode, d.Symbol, d.Timeframe,
		d.CandleCount, d.DataValidationResult,
		jsonOrNull(d.IndicatorSnapshot), jsonOrNull(d.RegimeSnapshot),
		jsonOrNull(d.StrategyAttempted),
		jsonOrNull(d.Candidate), jsonOrNull(d.ScoreBreakdown),
		jsonOrNull(d.NearMisses),
		jsonOrNull(d.RiskValidation), jsonOrNull(d.SafetyValidation),
		jsonOrNull(d.OrderPlan),
		jsonOrNull(d.LLMReview), d.LLMModeActive,
		d.FinalAction, d.FinalActionReason,
		d.EngineVersion, d.ScoringVersion, d.RiskConfigVersion, d.LLMPromptVersion,
	)
	if err != nil {
		return fmt.Errorf("insert decision_log: %w", err)
	}
	return nil
}

func (r *postgresDecisionLogRepository) GetByCycle(ctx context.Context, cycleID string) ([]domain.DecisionLog, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT decision_id, cycle_id, timestamp, mode, symbol, timeframe,
			candle_count, data_validation_result,
			COALESCE(indicator_snapshot::text,''), COALESCE(regime_snapshot::text,''),
			COALESCE(strategy_attempted::text,''),
			COALESCE(candidate::text,''), COALESCE(score_breakdown::text,''),
			COALESCE(near_misses::text,''),
			COALESCE(risk_validation::text,''), COALESCE(safety_validation::text,''),
			COALESCE(order_plan::text,''),
			COALESCE(llm_review::text,''), llm_mode_active,
			final_action, final_action_reason,
			engine_version, scoring_version, risk_config_version, llm_prompt_version
		FROM decision_logs WHERE cycle_id = $1 ORDER BY timestamp`, cycleID)
	if err != nil {
		return nil, fmt.Errorf("query decision_logs by cycle: %w", err)
	}
	defer rows.Close()
	return scanDecisionLogs(rows)
}

func (r *postgresDecisionLogRepository) GetRecent(ctx context.Context, limit int) ([]domain.DecisionLog, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT decision_id, cycle_id, timestamp, mode, symbol, timeframe,
			candle_count, data_validation_result,
			COALESCE(indicator_snapshot::text,''), COALESCE(regime_snapshot::text,''),
			COALESCE(strategy_attempted::text,''),
			COALESCE(candidate::text,''), COALESCE(score_breakdown::text,''),
			COALESCE(near_misses::text,''),
			COALESCE(risk_validation::text,''), COALESCE(safety_validation::text,''),
			COALESCE(order_plan::text,''),
			COALESCE(llm_review::text,''), llm_mode_active,
			final_action, final_action_reason,
			engine_version, scoring_version, risk_config_version, llm_prompt_version
		FROM decision_logs ORDER BY timestamp DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent decision_logs: %w", err)
	}
	defer rows.Close()
	return scanDecisionLogs(rows)
}

func (r *postgresDecisionLogRepository) GetBySymbol(ctx context.Context, symbol string, since time.Time) ([]domain.DecisionLog, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT decision_id, cycle_id, timestamp, mode, symbol, timeframe,
			candle_count, data_validation_result,
			COALESCE(indicator_snapshot::text,''), COALESCE(regime_snapshot::text,''),
			COALESCE(strategy_attempted::text,''),
			COALESCE(candidate::text,''), COALESCE(score_breakdown::text,''),
			COALESCE(near_misses::text,''),
			COALESCE(risk_validation::text,''), COALESCE(safety_validation::text,''),
			COALESCE(order_plan::text,''),
			COALESCE(llm_review::text,''), llm_mode_active,
			final_action, final_action_reason,
			engine_version, scoring_version, risk_config_version, llm_prompt_version
		FROM decision_logs WHERE symbol = $1 AND timestamp >= $2 ORDER BY timestamp DESC`, symbol, since)
	if err != nil {
		return nil, fmt.Errorf("query decision_logs by symbol: %w", err)
	}
	defer rows.Close()
	return scanDecisionLogs(rows)
}

func (r *postgresDecisionLogRepository) CountByAction(ctx context.Context, action string, since time.Time) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM decision_logs WHERE final_action = $1 AND timestamp >= $2`, action, since).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count decision_logs by action: %w", err)
	}
	return count, nil
}

func scanDecisionLogs(rows *sql.Rows) ([]domain.DecisionLog, error) {
	var logs []domain.DecisionLog
	for rows.Next() {
		var d domain.DecisionLog
		if err := rows.Scan(
			&d.DecisionID, &d.CycleID, &d.Timestamp, &d.Mode, &d.Symbol, &d.Timeframe,
			&d.CandleCount, &d.DataValidationResult,
			&d.IndicatorSnapshot, &d.RegimeSnapshot,
			&d.StrategyAttempted,
			&d.Candidate, &d.ScoreBreakdown,
			&d.NearMisses,
			&d.RiskValidation, &d.SafetyValidation,
			&d.OrderPlan,
			&d.LLMReview, &d.LLMModeActive,
			&d.FinalAction, &d.FinalActionReason,
			&d.EngineVersion, &d.ScoringVersion, &d.RiskConfigVersion, &d.LLMPromptVersion,
		); err != nil {
			return nil, fmt.Errorf("scan decision_log: %w", err)
		}
		logs = append(logs, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate decision_logs: %w", err)
	}
	return logs, nil
}

// --- CandidateOutcomeRepository ---

type postgresCandidateOutcomeRepository struct {
	db *sql.DB
}

func (r *postgresCandidateOutcomeRepository) Insert(ctx context.Context, o domain.CandidateOutcome) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO candidate_outcomes (
			decision_id, symbol, side, entry_price, stop_loss, take_profits,
			price_at_15m, price_at_1h, price_at_4h, price_at_24h,
			max_favorable_excursion_24h, max_adverse_excursion_24h,
			would_have_hit_tp1, would_have_hit_sl, would_have_outcome, result_in_r,
			tracked_until, status
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
		o.DecisionID, o.Symbol, string(o.Side), o.EntryPrice, o.StopLoss,
		o.TakeProfits,
		o.PriceAt15m, o.PriceAt1h, o.PriceAt4h, o.PriceAt24h,
		o.MaxFavorableExcursion24h, o.MaxAdverseExcursion24h,
		o.WouldHaveHitTP1, o.WouldHaveHitSL, o.WouldHaveOutcome, o.ResultInR,
		o.TrackedUntil, o.Status,
	)
	if err != nil {
		return fmt.Errorf("insert candidate_outcome: %w", err)
	}
	return nil
}

func (r *postgresCandidateOutcomeRepository) Update(ctx context.Context, o domain.CandidateOutcome) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE candidate_outcomes SET
			price_at_15m = $2, price_at_1h = $3, price_at_4h = $4, price_at_24h = $5,
			max_favorable_excursion_24h = $6, max_adverse_excursion_24h = $7,
			would_have_hit_tp1 = $8, would_have_hit_sl = $9,
			would_have_outcome = $10, result_in_r = $11,
			tracked_until = $12, status = $13
		WHERE decision_id = $1`,
		o.DecisionID,
		o.PriceAt15m, o.PriceAt1h, o.PriceAt4h, o.PriceAt24h,
		o.MaxFavorableExcursion24h, o.MaxAdverseExcursion24h,
		o.WouldHaveHitTP1, o.WouldHaveHitSL,
		o.WouldHaveOutcome, o.ResultInR,
		o.TrackedUntil, o.Status,
	)
	if err != nil {
		return fmt.Errorf("update candidate_outcome: %w", err)
	}
	return nil
}

func (r *postgresCandidateOutcomeRepository) GetPending(ctx context.Context) ([]domain.CandidateOutcome, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT decision_id, symbol, side, entry_price, stop_loss, COALESCE(take_profits::text,''),
			price_at_15m, price_at_1h, price_at_4h, price_at_24h,
			max_favorable_excursion_24h, max_adverse_excursion_24h,
			would_have_hit_tp1, would_have_hit_sl, would_have_outcome, result_in_r,
			tracked_until, status
		FROM candidate_outcomes WHERE status = 'tracking' ORDER BY decision_id`)
	if err != nil {
		return nil, fmt.Errorf("query pending candidate_outcomes: %w", err)
	}
	defer rows.Close()
	return scanCandidateOutcomes(rows)
}

func (r *postgresCandidateOutcomeRepository) GetByDecision(ctx context.Context, decisionID string) (*domain.CandidateOutcome, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT decision_id, symbol, side, entry_price, stop_loss, COALESCE(take_profits::text,''),
			price_at_15m, price_at_1h, price_at_4h, price_at_24h,
			max_favorable_excursion_24h, max_adverse_excursion_24h,
			would_have_hit_tp1, would_have_hit_sl, would_have_outcome, result_in_r,
			tracked_until, status
		FROM candidate_outcomes WHERE decision_id = $1`, decisionID)
	var o domain.CandidateOutcome
	var side string
	err := row.Scan(&o.DecisionID, &o.Symbol, &side, &o.EntryPrice, &o.StopLoss,
		&o.TakeProfits,
		&o.PriceAt15m, &o.PriceAt1h, &o.PriceAt4h, &o.PriceAt24h,
		&o.MaxFavorableExcursion24h, &o.MaxAdverseExcursion24h,
		&o.WouldHaveHitTP1, &o.WouldHaveHitSL, &o.WouldHaveOutcome, &o.ResultInR,
		&o.TrackedUntil, &o.Status,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get candidate_outcome: %w", err)
	}
	o.Side = domain.Side(side)
	return &o, nil
}

func (r *postgresCandidateOutcomeRepository) GetCompleted(ctx context.Context, since time.Time) ([]domain.CandidateOutcome, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT decision_id, symbol, side, entry_price, stop_loss, COALESCE(take_profits::text,''),
			price_at_15m, price_at_1h, price_at_4h, price_at_24h,
			max_favorable_excursion_24h, max_adverse_excursion_24h,
			would_have_hit_tp1, would_have_hit_sl, would_have_outcome, result_in_r,
			tracked_until, status
		FROM candidate_outcomes WHERE status = 'completed' AND tracked_until >= $1 ORDER BY tracked_until DESC`, since)
	if err != nil {
		return nil, fmt.Errorf("query completed candidate_outcomes: %w", err)
	}
	defer rows.Close()
	return scanCandidateOutcomes(rows)
}

func scanCandidateOutcomes(rows *sql.Rows) ([]domain.CandidateOutcome, error) {
	var outcomes []domain.CandidateOutcome
	for rows.Next() {
		var o domain.CandidateOutcome
		var side string
		if err := rows.Scan(&o.DecisionID, &o.Symbol, &side, &o.EntryPrice, &o.StopLoss,
			&o.TakeProfits,
			&o.PriceAt15m, &o.PriceAt1h, &o.PriceAt4h, &o.PriceAt24h,
			&o.MaxFavorableExcursion24h, &o.MaxAdverseExcursion24h,
			&o.WouldHaveHitTP1, &o.WouldHaveHitSL, &o.WouldHaveOutcome, &o.ResultInR,
			&o.TrackedUntil, &o.Status,
		); err != nil {
			return nil, fmt.Errorf("scan candidate_outcome: %w", err)
		}
		o.Side = domain.Side(side)
		outcomes = append(outcomes, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate candidate_outcomes: %w", err)
	}
	return outcomes, nil
}

// --- PaperTradeRepository ---

type postgresPaperTradeRepository struct {
	db *sql.DB
}

func (r *postgresPaperTradeRepository) Insert(ctx context.Context, t domain.PaperTrade) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO paper_trades (
			paper_trade_id, decision_id, opened_at, closed_at,
			symbol, side, qty, leverage, entry_price, exit_price,
			stop_loss, take_profit, fees_paid, funding_paid,
			pnl_gross, pnl_net, r_multiple, exit_reason
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
		t.PaperTradeID, t.DecisionID, t.OpenedAt, t.ClosedAt,
		t.Symbol, string(t.Side), t.Qty, t.Leverage, t.EntryPrice, t.ExitPrice,
		t.StopLoss, t.TakeProfit, t.FeesPaid, t.FundingPaid,
		t.PnLGross, t.PnLNet, t.RMultiple, t.ExitReason,
	)
	if err != nil {
		return fmt.Errorf("insert paper_trade: %w", err)
	}
	return nil
}

func (r *postgresPaperTradeRepository) Update(ctx context.Context, t domain.PaperTrade) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE paper_trades SET
			closed_at = $2, exit_price = $3, fees_paid = $4, funding_paid = $5,
			pnl_gross = $6, pnl_net = $7, r_multiple = $8, exit_reason = $9
		WHERE paper_trade_id = $1`,
		t.PaperTradeID, t.ClosedAt, t.ExitPrice, t.FeesPaid, t.FundingPaid,
		t.PnLGross, t.PnLNet, t.RMultiple, t.ExitReason,
	)
	if err != nil {
		return fmt.Errorf("update paper_trade: %w", err)
	}
	return nil
}

func (r *postgresPaperTradeRepository) GetOpen(ctx context.Context) ([]domain.PaperTrade, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT paper_trade_id, decision_id, opened_at, closed_at,
			symbol, side, qty, leverage, entry_price, exit_price,
			stop_loss, take_profit, fees_paid, funding_paid,
			pnl_gross, pnl_net, r_multiple, exit_reason
		FROM paper_trades WHERE closed_at IS NULL ORDER BY opened_at`)
	if err != nil {
		return nil, fmt.Errorf("query open paper_trades: %w", err)
	}
	defer rows.Close()
	return scanPaperTrades(rows)
}

func (r *postgresPaperTradeRepository) GetByDecision(ctx context.Context, decisionID string) (*domain.PaperTrade, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT paper_trade_id, decision_id, opened_at, closed_at,
			symbol, side, qty, leverage, entry_price, exit_price,
			stop_loss, take_profit, fees_paid, funding_paid,
			pnl_gross, pnl_net, r_multiple, exit_reason
		FROM paper_trades WHERE decision_id = $1`, decisionID)
	var t domain.PaperTrade
	var side string
	err := row.Scan(&t.PaperTradeID, &t.DecisionID, &t.OpenedAt, &t.ClosedAt,
		&t.Symbol, &side, &t.Qty, &t.Leverage, &t.EntryPrice, &t.ExitPrice,
		&t.StopLoss, &t.TakeProfit, &t.FeesPaid, &t.FundingPaid,
		&t.PnLGross, &t.PnLNet, &t.RMultiple, &t.ExitReason,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get paper_trade: %w", err)
	}
	t.Side = domain.Side(side)
	return &t, nil
}

func (r *postgresPaperTradeRepository) GetAll(ctx context.Context, since time.Time) ([]domain.PaperTrade, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT paper_trade_id, decision_id, opened_at, closed_at,
			symbol, side, qty, leverage, entry_price, exit_price,
			stop_loss, take_profit, fees_paid, funding_paid,
			pnl_gross, pnl_net, r_multiple, exit_reason
		FROM paper_trades WHERE closed_at IS NOT NULL AND closed_at >= $1 ORDER BY closed_at DESC`, since)
	if err != nil {
		return nil, fmt.Errorf("query paper_trades: %w", err)
	}
	defer rows.Close()
	return scanPaperTrades(rows)
}

func (r *postgresPaperTradeRepository) CountByExitReason(ctx context.Context, reason string, since time.Time) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM paper_trades WHERE exit_reason = $1 AND closed_at >= $2`, reason, since).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count paper_trades by exit_reason: %w", err)
	}
	return count, nil
}

func scanPaperTrades(rows *sql.Rows) ([]domain.PaperTrade, error) {
	var trades []domain.PaperTrade
	for rows.Next() {
		var t domain.PaperTrade
		var side string
		if err := rows.Scan(&t.PaperTradeID, &t.DecisionID, &t.OpenedAt, &t.ClosedAt,
			&t.Symbol, &side, &t.Qty, &t.Leverage, &t.EntryPrice, &t.ExitPrice,
			&t.StopLoss, &t.TakeProfit, &t.FeesPaid, &t.FundingPaid,
			&t.PnLGross, &t.PnLNet, &t.RMultiple, &t.ExitReason,
		); err != nil {
			return nil, fmt.Errorf("scan paper_trade: %w", err)
		}
		t.Side = domain.Side(side)
		trades = append(trades, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate paper_trades: %w", err)
	}
	return trades, nil
}

// --- PaperAccountStateRepository ---

type postgresPaperAccountStateRepository struct {
	db *sql.DB
}

func (r *postgresPaperAccountStateRepository) Get(ctx context.Context) (*domain.PaperAccountState, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, starting_equity, current_equity, total_trades, wins, losses, realized_pnl, consecutive_losses, updated_at
		FROM paper_account_state WHERE id = 1`)
	var s domain.PaperAccountState
	err := row.Scan(&s.ID, &s.StartingEquity, &s.CurrentEquity, &s.TotalTrades, &s.Wins, &s.Losses, &s.RealizedPnL, &s.ConsecutiveLosses, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		return &domain.PaperAccountState{ID: 1}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get paper_account_state: %w", err)
	}
	return &s, nil
}

func (r *postgresPaperAccountStateRepository) Update(ctx context.Context, s domain.PaperAccountState) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO paper_account_state (id, starting_equity, current_equity, total_trades, wins, losses, realized_pnl, consecutive_losses, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
		 ON CONFLICT (id) DO UPDATE SET
			starting_equity = EXCLUDED.starting_equity,
			current_equity = EXCLUDED.current_equity,
			total_trades = EXCLUDED.total_trades,
			wins = EXCLUDED.wins,
			losses = EXCLUDED.losses,
			realized_pnl = EXCLUDED.realized_pnl,
			consecutive_losses = EXCLUDED.consecutive_losses,
			updated_at = NOW()`,
		s.ID, s.StartingEquity, s.CurrentEquity, s.TotalTrades, s.Wins, s.Losses, s.RealizedPnL, s.ConsecutiveLosses,
	)
	if err != nil {
		return fmt.Errorf("update paper_account_state: %w", err)
	}
	return nil
}

func jsonOrNull(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
