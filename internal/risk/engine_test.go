package risk

import (
	"math"
	"testing"
	"time"

	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/domain"
)

func defaultConfig() app.UserConfig {
	return app.UserConfig{
		App: app.AppConfig{
			Mode: "paper",
		},
		MarketData: app.MarketDataConfig{
			StaleDataThresholdSeconds: 30,
		},
		Broker: app.BrokerConfig{
			Paper: app.PaperConfig{
				DefaultLeverage: 5,
			},
		},
		Universe: app.UniverseConfig{
			Filters: app.UniverseFiltersConfig{
				MaxSpreadBps: 50,
			},
		},
		Strategy: app.StrategyConfig{
			Indicators: app.IndicatorsConfig{
				MinRR:                      2.0,
				ExpectedMoveCostMultiplier: 3.0,
			},
		},
		PortfolioRisk: app.PortfolioRiskConfig{
			MaxOpenPositions:          3,
			MaxNewPositionsPerCycle:   2,
			MaxTotalExposureUSD:       5000,
			MaxTotalMarginUsedPct:     50,
			MaxRiskPerTradePct:        0.5,
			MaxDailyLossPct:           3,
			MaxSameDirectionPositions: 3,
			MaxPerSymbolPosition:      1,
			MinNotionalUSD:            10,
			MarginPerTradeUSD:         50,
			MaxLeverage:               10,
			CooldownAfterLosses: app.CooldownConfig{
				Enabled:           true,
				ConsecutiveLosses: 3,
				CooldownMinutes:   60,
			},
		},
		Sizing: app.SizingConfig{
			MaxLeverage:        10,
			MarginPerTradeUSD:  50,
			MaxRiskPerTradePct: 0.5,
		},
	}
}

func validInput() ValidateInput {
	entry := 65000.0
	sl := 64000.0
	tp := 67000.0
	return ValidateInput{
		ProposedTrade: domain.ProposedTrade{
			Symbol:             "BTCUSDT",
			Side:               domain.SideLong,
			ProposedEntry:      entry,
			ProposedStopLoss:   sl,
			ProposedTakeProfit: tp,
			RR:                 2.0,
			ExpectedMove:       2000,
			EstimatedTotalCost: 100,
			SetupScore:         80,
		},
		LLMDecision: domain.LLMDecision{
			Decision:       "ALLOW_MARKET",
			Confidence:     0.85,
			SizeMultiplier: 1.0,
		},
		AccountState: domain.AccountState{
			Balance:    1000,
			Equity:     1000,
			UsedMargin: 0,
		},
		MarketState: MarketState{
			Symbol:      "BTCUSDT",
			Price:       65000,
			SpreadBps:   10,
			SlippageBps: 5,
			DepthRatio:  0.5,
			LastUpdate:  time.Now(),
			Stale:       false,
		},
		Portfolio: PortfolioState{
			OpenPositions:         nil,
			NewPositionsThisCycle: 0,
			DailyLoss:             0,
		},
		BotState: domain.BotState{
			Running: true,
			Halted:  false,
		},
	}
}

func TestValidate_ApproveValidTrade(t *testing.T) {
	e := NewEngine(defaultConfig())
	output := e.Validate(validInput())
	if !output.Approved {
		t.Errorf("expected approved, got rejected: %v", output.ReasonCodes)
	}
	if output.FinalPositionNotional <= 0 {
		t.Error("expected positive notional")
	}
	if output.RequiredMargin <= 0 {
		t.Error("expected positive margin")
	}
}

func TestValidate_BotHalted(t *testing.T) {
	e := NewEngine(defaultConfig())
	input := validInput()
	input.BotState.Halted = true
	output := e.Validate(input)
	if output.Approved {
		t.Error("expected rejected: bot halted")
	}
	assertContainsReason(t, output.ReasonCodes, "BOT_HALTED")
}

func TestValidate_MarketDataStale(t *testing.T) {
	e := NewEngine(defaultConfig())
	input := validInput()
	input.MarketState.Stale = true
	output := e.Validate(input)
	if output.Approved {
		t.Error("expected rejected: stale data")
	}
	assertContainsReason(t, output.ReasonCodes, "MARKET_DATA_STALE")
}

func TestValidate_MarketDataStaleByTime(t *testing.T) {
	e := NewEngine(defaultConfig())
	input := validInput()
	input.MarketState.LastUpdate = time.Now().Add(-60 * time.Second)
	output := e.Validate(input)
	if output.Approved {
		t.Error("expected rejected: stale data by time")
	}
	assertContainsReason(t, output.ReasonCodes, "MARKET_DATA_STALE")
}

func TestValidate_SpreadTooWide(t *testing.T) {
	e := NewEngine(defaultConfig())
	input := validInput()
	input.MarketState.SpreadBps = 100
	output := e.Validate(input)
	if output.Approved {
		t.Error("expected rejected: spread too wide")
	}
	assertContainsReason(t, output.ReasonCodes, "SPREAD_TOO_WIDE")
}

func TestValidate_SlippageTooHigh(t *testing.T) {
	e := NewEngine(defaultConfig())
	input := validInput()
	input.MarketState.SlippageBps = 150
	output := e.Validate(input)
	if output.Approved {
		t.Error("expected rejected: slippage too high")
	}
	assertContainsReason(t, output.ReasonCodes, "SLIPPAGE_TOO_HIGH")
}

func TestValidate_DailyLossBreach(t *testing.T) {
	e := NewEngine(defaultConfig())
	input := validInput()
	input.Portfolio.DailyLoss = 50 // 5% of 1000 > 3% limit
	output := e.Validate(input)
	if output.Approved {
		t.Error("expected rejected: daily loss breached")
	}
	assertContainsReason(t, output.ReasonCodes, "DAILY_LOSS_BREACHED")
}

func TestValidate_MaxOpenPositions(t *testing.T) {
	e := NewEngine(defaultConfig())
	input := validInput()
	input.Portfolio.OpenPositions = []domain.Position{
		{Symbol: "A", Side: domain.SideLong, Status: domain.PositionStatusOpen},
		{Symbol: "B", Side: domain.SideLong, Status: domain.PositionStatusOpen},
		{Symbol: "C", Side: domain.SideLong, Status: domain.PositionStatusOpen},
	}
	output := e.Validate(input)
	if output.Approved {
		t.Error("expected rejected: max open positions")
	}
	assertContainsReason(t, output.ReasonCodes, "MAX_OPEN_POSITIONS")
}

func TestValidate_MaxNewPositionsPerCycle(t *testing.T) {
	e := NewEngine(defaultConfig())
	input := validInput()
	input.Portfolio.NewPositionsThisCycle = 2
	output := e.Validate(input)
	if output.Approved {
		t.Error("expected rejected: max new per cycle")
	}
	assertContainsReason(t, output.ReasonCodes, "MAX_NEW_POSITIONS_PER_CYCLE")
}

func TestValidate_DuplicateSymbol(t *testing.T) {
	e := NewEngine(defaultConfig())
	input := validInput()
	input.Portfolio.OpenPositions = []domain.Position{
		{Symbol: "BTCUSDT", Side: domain.SideLong, Status: domain.PositionStatusOpen},
	}
	output := e.Validate(input)
	if output.Approved {
		t.Error("expected rejected: duplicate symbol")
	}
	assertContainsReason(t, output.ReasonCodes, "DUPLICATE_SYMBOL")
}

func TestValidate_TooManySameDirection(t *testing.T) {
	e := NewEngine(defaultConfig())
	input := validInput()
	input.Portfolio.OpenPositions = []domain.Position{
		{Symbol: "ETHUSDT", Side: domain.SideLong, Status: domain.PositionStatusOpen},
		{Symbol: "SOLUSDT", Side: domain.SideLong, Status: domain.PositionStatusOpen},
		{Symbol: "DOGEUSDT", Side: domain.SideLong, Status: domain.PositionStatusOpen},
	}
	output := e.Validate(input)
	if output.Approved {
		t.Error("expected rejected: too many same direction")
	}
	assertContainsReason(t, output.ReasonCodes, "TOO_MANY_SAME_DIRECTION")
}

func TestValidate_InCooldown(t *testing.T) {
	e := NewEngine(defaultConfig())
	input := validInput()
	future := time.Now().Add(30 * time.Minute)
	input.Portfolio.CooldownUntil = &future
	output := e.Validate(input)
	if output.Approved {
		t.Error("expected rejected: in cooldown")
	}
	assertContainsReason(t, output.ReasonCodes, "IN_COOLDOWN")
}

func TestValidate_ConsecutiveLossesCooldown(t *testing.T) {
	e := NewEngine(defaultConfig())
	input := validInput()
	input.Portfolio.ConsecutiveLosses = 3
	output := e.Validate(input)
	if output.Approved {
		t.Error("expected rejected: consecutive losses")
	}
	assertContainsReason(t, output.ReasonCodes, "CONSECUTIVE_LOSSES_COOLDOWN")
}

func TestValidate_LongSLWrongSide(t *testing.T) {
	e := NewEngine(defaultConfig())
	input := validInput()
	input.ProposedTrade.ProposedStopLoss = 66000 // above entry for LONG
	output := e.Validate(input)
	if output.Approved {
		t.Error("expected rejected: SL wrong side")
	}
	assertContainsReason(t, output.ReasonCodes, "SL_WRONG_SIDE")
}

func TestValidate_ShortSLWrongSide(t *testing.T) {
	e := NewEngine(defaultConfig())
	input := validInput()
	input.ProposedTrade.Side = domain.SideShort
	input.ProposedTrade.ProposedEntry = 65000
	input.ProposedTrade.ProposedStopLoss = 64000 // below entry for SHORT
	output := e.Validate(input)
	if output.Approved {
		t.Error("expected rejected: SL wrong side for short")
	}
	assertContainsReason(t, output.ReasonCodes, "SL_WRONG_SIDE")
}

func TestValidate_SLMissing(t *testing.T) {
	e := NewEngine(defaultConfig())
	input := validInput()
	input.ProposedTrade.ProposedStopLoss = 0
	output := e.Validate(input)
	if output.Approved {
		t.Error("expected rejected: SL missing")
	}
	assertContainsReason(t, output.ReasonCodes, "SL_MISSING")
}

func TestValidate_RRBelowMin(t *testing.T) {
	e := NewEngine(defaultConfig())
	input := validInput()
	input.ProposedTrade.RR = 1.0
	output := e.Validate(input)
	if output.Approved {
		t.Error("expected rejected: RR below min")
	}
	assertContainsReason(t, output.ReasonCodes, "RR_BELOW_MIN")
}

func TestValidate_ExpectedMoveTooSmall(t *testing.T) {
	e := NewEngine(defaultConfig())
	input := validInput()
	input.ProposedTrade.ExpectedMove = 100 // < 3 * 100
	input.ProposedTrade.EstimatedTotalCost = 100
	output := e.Validate(input)
	if output.Approved {
		t.Error("expected rejected: expected move too small")
	}
	assertContainsReason(t, output.ReasonCodes, "EXPECTED_MOVE_TOO_SMALL")
}

func TestValidate_BelowMinNotional(t *testing.T) {
	cfg := defaultConfig()
	cfg.PortfolioRisk.MinNotionalUSD = 10000
	cfg.PortfolioRisk.MarginPerTradeUSD = 1
	cfg.Sizing.MarginPerTradeUSD = 1
	e := NewEngine(cfg)
	input := validInput()
	output := e.Validate(input)
	if output.Approved {
		t.Error("expected rejected: below min notional")
	}
	assertContainsReason(t, output.ReasonCodes, "BELOW_MIN_NOTIONAL")
}

func TestValidate_ReduceSizeMultiplier(t *testing.T) {
	e := NewEngine(defaultConfig())
	input := validInput()
	input.LLMDecision.Decision = "REDUCE_SIZE"
	input.LLMDecision.SizeMultiplier = 0.5
	output := e.Validate(input)
	if !output.Approved {
		t.Errorf("expected approved, got: %v", output.ReasonCodes)
	}
	// Notional should be halved
	baseInput := validInput()
	baseOutput := e.Validate(baseInput)
	if output.FinalPositionNotional >= baseOutput.FinalPositionNotional {
		t.Error("REDUCE_SIZE should produce smaller notional")
	}
}

func TestValidate_ShortTPWrongSide(t *testing.T) {
	e := NewEngine(defaultConfig())
	input := validInput()
	input.ProposedTrade.Side = domain.SideShort
	input.ProposedTrade.ProposedEntry = 65000
	input.ProposedTrade.ProposedStopLoss = 66000   // correct for SHORT (above entry)
	input.ProposedTrade.ProposedTakeProfit = 66000 // wrong: above entry for SHORT
	output := e.Validate(input)
	if output.Approved {
		t.Error("expected rejected: TP wrong side for short")
	}
	assertContainsReason(t, output.ReasonCodes, "TP_WRONG_SIDE")
}

func TestRankAndSelectPortfolio_ApprovesBest(t *testing.T) {
	e := NewEngine(defaultConfig())

	inputs := []ValidateInput{
		makeInput("BTCUSDT", 70),
		makeInput("ETHUSDT", 90),
		makeInput("SOLUSDT", 80),
	}

	results := e.RankAndSelectPortfolio(inputs)

	// ETHUSDT (score 90) should be rank 1, SOLUSDT (80) rank 2
	for _, r := range results {
		if r.Approved && r.PortfolioRank == 0 {
			t.Error("approved candidate should have portfolio_rank > 0")
		}
	}
}

func TestRankAndSelectPortfolio_LimitsNewPositions(t *testing.T) {
	cfg := defaultConfig()
	cfg.PortfolioRisk.MaxNewPositionsPerCycle = 1
	e := NewEngine(cfg)

	inputs := []ValidateInput{
		makeInput("BTCUSDT", 70),
		makeInput("ETHUSDT", 90),
		makeInput("SOLUSDT", 80),
	}

	results := e.RankAndSelectPortfolio(inputs)

	approvedCount := 0
	for _, r := range results {
		if r.Approved {
			approvedCount++
		}
	}
	if approvedCount != 1 {
		t.Errorf("expected 1 approved, got %d", approvedCount)
	}
}

func TestRankAndSelectPortfolio_RejectsWithPortfolioLimit(t *testing.T) {
	cfg := defaultConfig()
	cfg.PortfolioRisk.MaxNewPositionsPerCycle = 1
	e := NewEngine(cfg)

	inputs := []ValidateInput{
		makeInput("BTCUSDT", 70),
		makeInput("ETHUSDT", 90),
	}

	results := e.RankAndSelectPortfolio(inputs)

	for _, r := range results {
		if !r.Approved {
			found := false
			for _, code := range r.ReasonCodes {
				if code == "PORTFOLIO_RISK_LIMIT" {
					found = true
				}
			}
			if !found {
				t.Error("rejected candidate should have PORTFOLIO_RISK_LIMIT reason")
			}
		}
	}
}

func makeInput(symbol string, setupScore float64) ValidateInput {
	input := validInput()
	input.ProposedTrade.Symbol = symbol
	input.ProposedTrade.SetupScore = setupScore
	return input
}

func assertContainsReason(t *testing.T, reasons []string, expected string) {
	t.Helper()
	for _, r := range reasons {
		if r == expected {
			return
		}
	}
	t.Errorf("expected reason %q in %v", expected, reasons)
}

func TestValidate_RejectsBLOCKDecision(t *testing.T) {
	e := NewEngine(defaultConfig())
	input := validInput()
	input.LLMDecision.Decision = "BLOCK"
	input.LLMDecision.Confidence = 0.9
	output := e.Validate(input)
	if output.Approved {
		t.Error("expected rejected: BLOCK decision")
	}
	assertContainsReason(t, output.ReasonCodes, "LLM_DECISION_BLOCK")
}

func TestValidate_RejectsConfidenceZero(t *testing.T) {
	e := NewEngine(defaultConfig())
	input := validInput()
	input.LLMDecision.Confidence = 0
	output := e.Validate(input)
	if output.Approved {
		t.Error("expected rejected: confidence 0")
	}
	assertContainsReason(t, output.ReasonCodes, "LLM_LOW_CONFIDENCE")
}

func TestValidate_RejectsConfidenceBelowThreshold(t *testing.T) {
	e := NewEngine(defaultConfig())
	input := validInput()
	input.LLMDecision.Confidence = 0.59
	output := e.Validate(input)
	if output.Approved {
		t.Error("expected rejected: confidence 0.59")
	}
	assertContainsReason(t, output.ReasonCodes, "LLM_LOW_CONFIDENCE")
}

func TestValidate_AcceptsConfidenceAtThreshold(t *testing.T) {
	e := NewEngine(defaultConfig())
	input := validInput()
	input.LLMDecision.Confidence = 0.6
	output := e.Validate(input)
	if !output.Approved {
		t.Errorf("expected approved at confidence 0.6, got rejected: %v", output.ReasonCodes)
	}
}

func TestRiskEngine_RejectNaNEquity(t *testing.T) {
	e := NewEngine(defaultConfig())
	input := validInput()
	input.AccountState.Equity = math.NaN()
	output := e.Validate(input)
	if output.Approved {
		t.Fatal("expected rejection for NaN equity")
	}
	assertContainsReason(t, output.ReasonCodes, "INVALID_EQUITY")
}

func TestRiskEngine_RejectZeroEntry(t *testing.T) {
	e := NewEngine(defaultConfig())
	input := validInput()
	input.ProposedTrade.ProposedEntry = 0
	output := e.Validate(input)
	if output.Approved {
		t.Fatal("expected rejection for zero entry")
	}
	assertContainsReason(t, output.ReasonCodes, "INVALID_ENTRY")
}
