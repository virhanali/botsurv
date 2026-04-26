package domain

import "time"

// Candle represents a single OHLCV candle.
type Candle struct {
	Symbol    string
	Timeframe string
	OpenTime  int64
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64
	TurnOver  float64
	Confirmed bool
}

// SymbolInfo contains static exchange metadata for a symbol.
type SymbolInfo struct {
	Symbol      string
	Status      string
	QuoteAsset  string
	BaseAsset   string
	MinNotional float64
	TickSize    float64
	LotSize     float64
	MaxLeverage float64
}

// UniverseSymbol extends SymbolInfo with scanner state.
type UniverseSymbol struct {
	SymbolInfo
	Blacklist      bool
	ForceInclude   bool
	LiquidityScore float64
	LastScanAt     time.Time
}

// Side represents trade direction.
type Side string

const (
	SideLong  Side = "LONG"
	SideShort Side = "SHORT"
)

// EntryType represents how the trade should be entered.
type EntryType string

const (
	EntryTypeMarket      EntryType = "MARKET"
	EntryTypeLimitRetest EntryType = "LIMIT_RETEST"
)

// ProposedTrade is the deterministic output of the Setup Engine.
type ProposedTrade struct {
	Symbol             string
	Side               Side
	SetupType          string
	Regime             string
	EntryType          EntryType
	ProposedEntry      float64
	ProposedStopLoss   float64
	ProposedTakeProfit float64
	StopLossPct        float64
	TakeProfitPct      float64
	RR                 float64
	InvalidationLevel  float64
	ReasonCodes        []string
	SetupScore         float64
	ExpectedMove       float64
	EstimatedTotalCost float64
}

// Candidate is a proposed trade that has passed universe scanning.
type Candidate struct {
	ProposedTrade
	CycleID               string
	CandidateScore        float64
	LiquidityScore        float64
	ExecutionScore        float64
	VolatilityScore       float64
	LLMEligible           bool
	LLMRoutingReasonCodes []string
}

// LLMDecision is the parsed response from the LLM Veto Agent.
type LLMDecision struct {
	Decision         string
	Confidence       float64
	SizeMultiplier   float64
	Regime           string
	ReasonCodes      []string
	RiskFlags        []string
	Notes            string
	RawResponse      string
	ValidationStatus string
}

// RiskDecision is the output of the Risk Engine.
type RiskDecision struct {
	Approved              bool
	FinalPositionNotional float64
	RequiredMargin        float64
	EstimatedLoss         float64
	ReasonCodes           []string
	PortfolioRank         int
	PortfolioRejectReason string
}

// OrderSide represents the side of an order.
type OrderSide string

const (
	OrderSideBuy  OrderSide = "BUY"
	OrderSideSell OrderSide = "SELL"
)

// OrderType represents the type of order.
type OrderType string

const (
	OrderTypeMarket           OrderType = "MARKET"
	OrderTypeLimit            OrderType = "LIMIT"
	OrderTypeStopMarket       OrderType = "STOP_MARKET"
	OrderTypeTakeProfitMarket OrderType = "TAKE_PROFIT_MARKET"
)

// OrderStatus represents the lifecycle state of an order.
type OrderStatus string

const (
	OrderStatusPending         OrderStatus = "pending"
	OrderStatusFilled          OrderStatus = "filled"
	OrderStatusPartiallyFilled OrderStatus = "partially_filled"
	OrderStatusCancelled       OrderStatus = "cancelled"
	OrderStatusRejected        OrderStatus = "rejected"
)

// Order represents a broker order.
type Order struct {
	ID            int64
	BrokerOrderID string
	PositionID    *int64
	Symbol        string
	Side          OrderSide
	OrderType     OrderType
	Qty           float64
	Price         *float64
	StopPrice     *float64
	Status        OrderStatus
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// PositionStatus represents the state of a position.
type PositionStatus string

const (
	PositionStatusOpen   PositionStatus = "open"
	PositionStatusClosed PositionStatus = "closed"
)

// Position represents an open or closed futures position.
type Position struct {
	ID            int64
	Symbol        string
	Side          Side
	EntryPrice    float64
	Size          float64
	Leverage      float64
	Margin        float64
	StopLoss      float64
	TakeProfit    float64
	SLOrderID     *int64
	TPOrderID     *int64
	UnrealizedPnL float64
	RealizedPnL   float64
	Status        PositionStatus
	Source        string
	OpenedAt      time.Time
	ClosedAt      *time.Time
}

// Execution represents a single fill.
type Execution struct {
	ID         int64
	OrderID    int64
	Symbol     string
	Side       OrderSide
	Qty        float64
	Price      float64
	Fee        float64
	Slippage   float64
	ExecutedAt time.Time
}

// PnLEvent represents a realized profit/loss record.
type PnLEvent struct {
	ID             int64
	PositionID     int64
	Symbol         string
	RealizedPnL    float64
	FeeImpact      float64
	SlippageImpact float64
	RecordedAt     time.Time
}

// AccountState represents the current state of the trading account.
type AccountState struct {
	ID               int64
	Balance          float64
	AvailableBalance float64
	UsedMargin       float64
	Equity           float64
	RealizedPnL      float64
	UnrealizedPnL    float64
	TotalFees        float64
	TotalSlippage    float64
	DailyLoss        float64
	RecordedAt       time.Time
}

// BotState represents the runtime state of the bot.
type BotState struct {
	Mode         string
	Running      bool
	LastCycleAt  *time.Time
	DailyResetAt *time.Time
	Halted       bool
	HaltReason   string
}

// MarketDataHealth represents the health of market data for a symbol.
type MarketDataHealth struct {
	Symbol           string
	Healthy          bool
	LastUpdate       time.Time
	StaleReason      string
	CandlesHealthy   bool
	OrderBookHealthy bool
	PriceHealthy     bool
}

// Cycle represents a single trading cycle.
type Cycle struct {
	ID          int64
	CycleID     string
	StartedAt   time.Time
	EndedAt     *time.Time
	Status      string
	ReasonCodes []string
}
