package risk

import (
	"math"
	"testing"
	"time"

	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/regime"
	"github.com/virhan/botsurv/internal/scoring"
	"github.com/virhan/botsurv/internal/strategy"
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

// --- Phase 4 Candidate Risk Validation Tests ---

func phase4Config() app.UserConfig {
	cfg := defaultConfig()
	cfg.Risk = app.RiskConfig{
		BaseRiskPerTradePct:    0.5,
		MaxRiskPerTradePct:     1.0,
		MaxLeverage:            5,
		PreferredLeverage:      3,
		MinRR:                  1.4,
		MaxOpenPositions:       2,
		MaxCorrelatedPositions: 1,
		DailyMaxLossPct:        3.0,
		WeeklyMaxLossPct:       6.0,
		MinRiskPerTradePct:     0.1,
		RiskConfigVersion:      "v1.0.0",
	}
	cfg.Broker.Paper.FeeTakerBps = 5.5
	return cfg
}

func phase4Input() CandidateRiskInput {
	return CandidateRiskInput{
		Candidate: strategy.TradeCandidate{
			CandidateID: "test-cand-1",
			Symbol:      "BTCUSDT",
			Side:        domain.SideLong,
			EntryType:   strategy.CandidateEntryMarket,
			EntryPrice:  50000,
			StopLoss:    49000,
			TakeProfits: []strategy.TakeProfitTarget{
				{Price: 52000, SizePct: 50},
				{Price: 53000, SizePct: 50},
			},
			RiskRewardRatio: 2.0,
		},
		ScoreResult: scoring.ScoreResult{Action: scoring.ActionAllowMarket, SizeMultiplier: 1.0},
		RegimeSnapshot: regime.MarketRegimeSnapshot{
			RelativeStrength: regime.RelativeStrengthSnapshot{Classification: "neutral"},
		},
		SymbolInfo: domain.SymbolInfo{
			Symbol:      "BTCUSDT",
			TickSize:    0.5,
			LotSize:     0.001,
			MinNotional: 10,
			MaxLeverage: 100,
		},
		AccountState: domain.AccountState{Equity: 10000, AvailableBalance: 10000, Balance: 10000},
		Portfolio:    PortfolioState{},
		BotState:     domain.BotState{Running: true},
	}
}

func TestValidateCandidate_ApproveValidTrade(t *testing.T) {
	e := NewEngine(phase4Config())
	input := phase4Input()
	result := e.ValidateCandidate(input)
	if !result.Approved {
		t.Fatalf("expected approved, got rejected: %v", result.RejectionReasons)
	}
	if result.OrderPlan == nil {
		t.Fatal("expected order plan")
	}
	// risk_amount = 10000 * 0.5 / 100 = 50
	// risk_per_unit = 50000 - 49000 = 1000
	// qty_raw = 50 / 1000 = 0.05 -> round to lot 0.001 -> 0.05
	// position_value = 0.05 * 50000 = 2500
	// margin = 2500 / 3 = 833.33
	expectedQty := 0.05
	if math.Abs(result.OrderPlan.Qty-expectedQty) > 1e-9 {
		t.Errorf("expected qty %.4f, got %.4f", expectedQty, result.OrderPlan.Qty)
	}
	if result.OrderPlan.Leverage != 3 {
		t.Errorf("expected leverage 3, got %f", result.OrderPlan.Leverage)
	}
	if result.RiskConfigVersion != "v1.0.0" {
		t.Errorf("expected version v1.0.0, got %s", result.RiskConfigVersion)
	}
}

func TestValidateCandidate_SizingKnownValues(t *testing.T) {
	e := NewEngine(phase4Config())
	input := phase4Input()
	input.Candidate.EntryPrice = 100000
	input.Candidate.StopLoss = 99000
	input.Candidate.TakeProfits = []strategy.TakeProfitTarget{
		{Price: 102000, SizePct: 50},
		{Price: 103000, SizePct: 50},
	}
	input.AccountState.Equity = 20000
	// risk_amount = 20000 * 0.5 / 100 = 100
	// risk_per_unit = 1000
	// qty_raw = 0.1 -> round to lot 0.001 -> 0.1
	result := e.ValidateCandidate(input)
	if !result.Approved {
		t.Fatalf("expected approved, got: %v", result.RejectionReasons)
	}
	expectedQty := 0.1
	if math.Abs(result.OrderPlan.Qty-expectedQty) > 1e-9 {
		t.Errorf("expected qty %.4f, got %.4f", expectedQty, result.OrderPlan.Qty)
	}
	expectedMargin := (0.1 * 100000) / 3
	if math.Abs(result.OrderPlan.MarginRequired-expectedMargin) > 1e-6 {
		t.Errorf("expected margin %.2f, got %.2f", expectedMargin, result.OrderPlan.MarginRequired)
	}
}

func TestValidateCandidate_ModifierReduceSize(t *testing.T) {
	e := NewEngine(phase4Config())
	input := phase4Input()
	input.ScoreResult.Action = scoring.ActionReduceSize
	result := e.ValidateCandidate(input)
	if !result.Approved {
		t.Fatalf("expected approved, got: %v", result.RejectionReasons)
	}
	found := false
	for _, m := range result.ModifiersApplied {
		if m == "scoring_reduce_size_0.5x" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected reduce_size modifier, got %v", result.ModifiersApplied)
	}
	// Base qty without modifier = 0.05, with 0.5x risk -> 0.025
	expectedQty := 0.025
	if math.Abs(result.OrderPlan.Qty-expectedQty) > 1e-9 {
		t.Errorf("expected qty %.4f after reduce, got %.4f", expectedQty, result.OrderPlan.Qty)
	}
}

func TestValidateCandidate_ModifierBTCBearishHTF(t *testing.T) {
	e := NewEngine(phase4Config())
	input := phase4Input()
	input.RegimeSnapshot.BTCFiltersTriggered = []string{"BTCStronglyBearishHTF"}
	result := e.ValidateCandidate(input)
	if !result.Approved {
		t.Fatalf("expected approved, got: %v", result.RejectionReasons)
	}
	found := false
	for _, m := range result.ModifiersApplied {
		if m == "btc_bearish_htf_0.7x" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected btc_bearish_htf modifier, got %v", result.ModifiersApplied)
	}
}

func TestValidateCandidate_ModifierStrongUnderperform(t *testing.T) {
	e := NewEngine(phase4Config())
	input := phase4Input()
	input.RegimeSnapshot.RelativeStrength.Classification = "strong_underperform"
	result := e.ValidateCandidate(input)
	if !result.Approved {
		t.Fatalf("expected approved, got: %v", result.RejectionReasons)
	}
	found := false
	for _, m := range result.ModifiersApplied {
		if m == "strong_underperform_0.6x" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected strong_underperform modifier, got %v", result.ModifiersApplied)
	}
}

func TestValidateCandidate_ModifierOrder(t *testing.T) {
	e := NewEngine(phase4Config())
	input := phase4Input()
	input.ScoreResult.Action = scoring.ActionReduceSize
	input.RegimeSnapshot.BTCFiltersTriggered = []string{"BTCStronglyBearishHTF"}
	input.RegimeSnapshot.RelativeStrength.Classification = "strong_underperform"
	result := e.ValidateCandidate(input)
	if !result.Approved {
		t.Fatalf("expected approved, got: %v", result.RejectionReasons)
	}
	// Order: reduce_size (0.5) -> bearish (0.7) -> underperform (0.6)
	// total multiplier = 0.5 * 0.7 * 0.6 = 0.21
	// base risk = 0.5%, final = 0.105% which is > min 0.1%
	expectedRiskPct := 0.5 * 0.5 * 0.7 * 0.6
	if math.Abs(result.OrderPlan.RiskPctUsed-expectedRiskPct) > 1e-9 {
		t.Errorf("expected risk pct %.4f, got %.4f", expectedRiskPct, result.OrderPlan.RiskPctUsed)
	}
	// Modifiers should be in order
	if len(result.ModifiersApplied) != 3 {
		t.Errorf("expected 3 modifiers, got %d: %v", len(result.ModifiersApplied), result.ModifiersApplied)
	}
	if result.ModifiersApplied[0] != "scoring_reduce_size_0.5x" {
		t.Errorf("expected first modifier scoring_reduce_size, got %s", result.ModifiersApplied[0])
	}
}

func TestValidateCandidate_RejectRiskTooSmall(t *testing.T) {
	cfg := phase4Config()
	cfg.Risk.MinRiskPerTradePct = 0.2 // set high so modifiers push below
	e := NewEngine(cfg)
	input := phase4Input()
	input.ScoreResult.Action = scoring.ActionReduceSize
	input.RegimeSnapshot.BTCFiltersTriggered = []string{"BTCStronglyBearishHTF"}
	input.RegimeSnapshot.RelativeStrength.Classification = "strong_underperform"
	// base 0.5 * 0.5 * 0.7 * 0.6 = 0.105 < 0.2 -> should reject
	result := e.ValidateCandidate(input)
	if result.Approved {
		t.Fatal("expected rejection for risk too small")
	}
	assertContainsReason(t, result.RejectionReasons, "RISK_TOO_SMALL")
}

func TestValidateCandidate_RejectSLWrongSideLong(t *testing.T) {
	e := NewEngine(phase4Config())
	input := phase4Input()
	input.Candidate.StopLoss = 51000 // above entry for LONG
	result := e.ValidateCandidate(input)
	if result.Approved {
		t.Fatal("expected rejection for SL wrong side")
	}
	assertContainsReason(t, result.RejectionReasons, "SL_WRONG_SIDE")
}

func TestValidateCandidate_RejectSLWrongSideShort(t *testing.T) {
	e := NewEngine(phase4Config())
	input := phase4Input()
	input.Candidate.Side = domain.SideShort
	input.Candidate.StopLoss = 49000 // below entry for SHORT
	result := e.ValidateCandidate(input)
	if result.Approved {
		t.Fatal("expected rejection for SL wrong side short")
	}
	assertContainsReason(t, result.RejectionReasons, "SL_WRONG_SIDE")
}

func TestValidateCandidate_RejectTPWrongSideLong(t *testing.T) {
	e := NewEngine(phase4Config())
	input := phase4Input()
	input.Candidate.TakeProfits[0].Price = 48000 // below entry for LONG
	result := e.ValidateCandidate(input)
	if result.Approved {
		t.Fatal("expected rejection for TP wrong side")
	}
	assertContainsReason(t, result.RejectionReasons, "TP_WRONG_SIDE")
}

func TestValidateCandidate_RejectRRBelowMin(t *testing.T) {
	e := NewEngine(phase4Config())
	input := phase4Input()
	input.Candidate.RiskRewardRatio = 1.2
	result := e.ValidateCandidate(input)
	if result.Approved {
		t.Fatal("expected rejection for RR below min")
	}
	assertContainsReason(t, result.RejectionReasons, "RR_BELOW_MIN")
}

func TestValidateCandidate_RejectMarginExceedsAvailable(t *testing.T) {
	e := NewEngine(phase4Config())
	input := phase4Input()
	input.AccountState.AvailableBalance = 100
	result := e.ValidateCandidate(input)
	if result.Approved {
		t.Fatal("expected rejection for margin exceeds available")
	}
	assertContainsReason(t, result.RejectionReasons, "MARGIN_EXCEEDS_AVAILABLE")
}

func TestValidateCandidate_RejectInvalidQty(t *testing.T) {
	e := NewEngine(phase4Config())
	input := phase4Input()
	input.SymbolInfo.LotSize = 10.0
	input.Candidate.EntryPrice = 50000
	input.Candidate.StopLoss = 49999 // tiny stop -> qty_raw very small
	input.AccountState.Equity = 10   // tiny equity -> qty_raw = 0.05
	// qty_raw = 0.05, rounded to lot 10.0 -> 0.0
	result := e.ValidateCandidate(input)
	if result.Approved {
		t.Fatal("expected rejection for invalid qty")
	}
	assertContainsReason(t, result.RejectionReasons, "INVALID_QTY")
}

func TestValidateCandidate_RejectBelowMinNotional(t *testing.T) {
	e := NewEngine(phase4Config())
	input := phase4Input()
	input.SymbolInfo.MinNotional = 1000000
	result := e.ValidateCandidate(input)
	if result.Approved {
		t.Fatal("expected rejection for below min notional")
	}
	assertContainsReason(t, result.RejectionReasons, "BELOW_MIN_NOTIONAL")
}

func TestValidateCandidate_RejectMaxOpenPositions(t *testing.T) {
	e := NewEngine(phase4Config())
	input := phase4Input()
	input.Portfolio.OpenPositions = []domain.Position{
		{Symbol: "ETHUSDT", Status: domain.PositionStatusOpen},
		{Symbol: "SOLUSDT", Status: domain.PositionStatusOpen},
	}
	result := e.ValidateCandidate(input)
	if result.Approved {
		t.Fatal("expected rejection for max open positions")
	}
	assertContainsReason(t, result.RejectionReasons, "MAX_OPEN_POSITIONS")
}

func TestValidateCandidate_RejectMaxCorrelatedAltPositions(t *testing.T) {
	e := NewEngine(phase4Config())
	input := phase4Input()
	input.Candidate.Symbol = "ETHUSDT"
	input.Portfolio.OpenPositions = []domain.Position{
		{Symbol: "SOLUSDT", Side: domain.SideLong, Status: domain.PositionStatusOpen},
	}
	result := e.ValidateCandidate(input)
	if result.Approved {
		t.Fatal("expected rejection for max correlated alt positions")
	}
	assertContainsReason(t, result.RejectionReasons, "MAX_CORRELATED_ALT_POSITIONS")
}

func TestValidateCandidate_BTCNotCountedAsCorrelatedAlt(t *testing.T) {
	e := NewEngine(phase4Config())
	input := phase4Input()
	input.Candidate.Symbol = "BTCUSDT"
	input.Portfolio.OpenPositions = []domain.Position{
		{Symbol: "ETHUSDT", Side: domain.SideLong, Status: domain.PositionStatusOpen},
	}
	result := e.ValidateCandidate(input)
	if !result.Approved {
		t.Fatalf("expected BTC to bypass correlated alt check, got: %v", result.RejectionReasons)
	}
}

func TestValidateCandidate_RejectBotHalted(t *testing.T) {
	e := NewEngine(phase4Config())
	input := phase4Input()
	input.BotState.Halted = true
	result := e.ValidateCandidate(input)
	if result.Approved {
		t.Fatal("expected rejection for bot halted")
	}
	assertContainsReason(t, result.RejectionReasons, "BOT_HALTED")
}

func TestValidateCandidate_TickSizeRounding(t *testing.T) {
	e := NewEngine(phase4Config())
	input := phase4Input()
	input.SymbolInfo.TickSize = 10
	input.Candidate.EntryPrice = 50005
	result := e.ValidateCandidate(input)
	if !result.Approved {
		t.Fatalf("expected approved, got: %v", result.RejectionReasons)
	}
	// 50005 rounded to tick size 10 -> 50010
	if result.OrderPlan.EntryPrice != 50010 {
		t.Errorf("expected entry price 50010, got %f", result.OrderPlan.EntryPrice)
	}
}

func TestValidateCandidate_LotSizeRounding(t *testing.T) {
	e := NewEngine(phase4Config())
	input := phase4Input()
	input.SymbolInfo.LotSize = 0.01
	input.Candidate.EntryPrice = 50000
	input.Candidate.StopLoss = 49000
	// qty_raw = 0.05, rounded to lot 0.01 -> 0.05 (exact)
	result := e.ValidateCandidate(input)
	if !result.Approved {
		t.Fatalf("expected approved, got: %v", result.RejectionReasons)
	}
	if result.OrderPlan.Qty != 0.05 {
		t.Errorf("expected qty 0.05, got %f", result.OrderPlan.Qty)
	}
}

func TestValidateCandidate_LotSizeRoundingEdgeCase(t *testing.T) {
	e := NewEngine(phase4Config())
	input := phase4Input()
	input.SymbolInfo.LotSize = 0.03
	input.Candidate.EntryPrice = 50000
	input.Candidate.StopLoss = 49000
	// qty_raw = 0.05, rounded to lot 0.03 -> 0.06 (round up)
	result := e.ValidateCandidate(input)
	if !result.Approved {
		t.Fatalf("expected approved, got: %v", result.RejectionReasons)
	}
	if result.OrderPlan.Qty != 0.06 {
		t.Errorf("expected qty 0.06, got %f", result.OrderPlan.Qty)
	}
}

func TestValidateCandidate_TakeProfitsDistributed(t *testing.T) {
	e := NewEngine(phase4Config())
	input := phase4Input()
	result := e.ValidateCandidate(input)
	if !result.Approved {
		t.Fatalf("expected approved, got: %v", result.RejectionReasons)
	}
	if len(result.OrderPlan.TakeProfits) != 2 {
		t.Fatalf("expected 2 take profits, got %d", len(result.OrderPlan.TakeProfits))
	}
	tp1 := result.OrderPlan.TakeProfits[0]
	tp2 := result.OrderPlan.TakeProfits[1]
	if tp1.Price != 52000 || tp2.Price != 53000 {
		t.Errorf("expected TP prices 52000/53000, got %.2f/%.2f", tp1.Price, tp2.Price)
	}
	// Each should get roughly half the qty
	if tp1.Qty <= 0 || tp2.Qty <= 0 {
		t.Errorf("expected positive TP qtys, got %.4f/%.4f", tp1.Qty, tp2.Qty)
	}
}

func TestRoundToTickSize(t *testing.T) {
	cases := []struct {
		price    float64
		tickSize float64
		want     float64
	}{
		{50005, 10, 50010},
		{50004, 10, 50000},
		{50000, 10, 50000},
		{123.456, 0.01, 123.46},
		{123.451, 0.01, 123.45},
		{100, 0, 100},
	}
	for _, c := range cases {
		got := roundToTickSize(c.price, c.tickSize)
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("roundToTickSize(%f, %f) = %f, want %f", c.price, c.tickSize, got, c.want)
		}
	}
}

func TestRoundToLotSize(t *testing.T) {
	cases := []struct {
		qty     float64
		lotSize float64
		want    float64
	}{
		{0.05, 0.001, 0.05},
		{0.051, 0.01, 0.05},
		{0.056, 0.01, 0.06},
		{1.23, 0.5, 1.0},
		{1.26, 0.5, 1.5},
		{100, 0, 100},
	}
	for _, c := range cases {
		got := roundToLotSize(c.qty, c.lotSize)
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("roundToLotSize(%f, %f) = %f, want %f", c.qty, c.lotSize, got, c.want)
		}
	}
}
