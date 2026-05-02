package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/logger"
	"github.com/virhan/botsurv/internal/shadow"
)

// Reviewer is the LLM Reviewer orchestrator.
// It manages mode-based behavior, request construction, validation,
// fallback logic, and cost tracking.
type Reviewer struct {
	cfg           app.LLMReviewConfig
	client        ReviewClient
	log           *logger.Logger
	mode          LMReviewMode

	// Stats
	invalidCount int64
	totalCalls   int64
}

// NewReviewer creates a new LLM Reviewer.
func NewReviewer(cfg app.LLMReviewConfig, log *logger.Logger) *Reviewer {
	cfg = cfg.WithDefaults()
	mode := LMReviewMode(cfg.Mode)
	if mode == "" {
		mode = ReviewModeOff
	}

	var client ReviewClient
	if mode != ReviewModeOff {
		client = NewReviewClient(cfg, log)
	}

	return &Reviewer{
		cfg:    cfg,
		client: client,
		log:    log,
		mode:   mode,
	}
}

// Mode returns the current review mode.
func (r *Reviewer) Mode() LMReviewMode { return r.mode }

// IsEnabled returns true if the reviewer is active (not off).
func (r *Reviewer) IsEnabled() bool { return r.mode != ReviewModeOff }

// InvalidCount returns the count of invalid LLM responses.
func (r *Reviewer) InvalidCount() int64 { return r.invalidCount }

// TotalCalls returns the total number of LLM review calls.
func (r *Reviewer) TotalCalls() int64 { return r.totalCalls }

// DailyCost returns the accumulated daily cost.
func (r *Reviewer) DailyCost() float64 {
	if r.client != nil {
		return r.client.DailyCost()
	}
	return 0
}

// ResetDailyCost resets the daily cost counter.
func (r *Reviewer) ResetDailyCost() {
	if r.client != nil {
		r.client.ResetDailyCost()
	}
}

// ReviewCandidateInput contains all the data needed to build a review request
// and review a candidate.
type ReviewCandidateInput struct {
	Symbol           string
	Side             string
	StrategyName     string
	Timeframe        string
	EntryType        string
	EntryPrice       float64
	StopLoss         float64
	TakeProfits      []ReviewTP
	RRRatio          float64
	TAReasoning      interface{}
	TotalScore       float64
	ScoreComponents  map[string]float64
	ScoringVersion   string
	Trend15m         string
	Trend1h          string
	Trend4h          string
	RSI14            float64
	RSIState         string
	MACDHistogram    float64
	MACDDirection    string
	ATRPct           float64
	VolumeRatio      float64
	EMAAlignment     map[string]bool
	BTCTrend1h       string
	BTCTrend4h       string
	BTCFiltersTrig   []string
	BTCDTrend        string
	BTCDAvailable    bool
	RelativeStrength float64
	RSClass          string
	Qty              float64
	Leverage         float64
	RiskAmountUSD    float64
	RiskPct          float64
	ModifiersApplied []string
}

// Review reviews a candidate and returns the outcome.
// The outcome depends on the LLM_MODE:
//   - off:        returns APPROVE immediately (LLM not called)
//   - audit_only: calls LLM, logs result, always returns APPROVE
//   - veto:       calls LLM, can REJECT or REDUCE_SIZE
//   - review:     calls LLM, can REJECT, REDUCE_SIZE, or APPROVE_RETEST_ONLY
func (r *Reviewer) Review(ctx context.Context, input ReviewCandidateInput) (*ReviewOutcome, error) {
	r.totalCalls++

	requestID := shadow.NewUUID()
	candidateID := fmt.Sprintf("cand-%s-%d", input.Symbol, time.Now().UnixNano())
	decisionID := shadow.NewUUID()

	outcome := &ReviewOutcome{
		CandidateID:   candidateID,
		Action:        ActionApprove,
		SizeMultiplier: 1.0,
	}

	// Mode off: skip LLM entirely
	if r.mode == ReviewModeOff || r.client == nil {
		outcome.Result = &ReviewResult{Called: false, Valid: false, InvalidReason: "MODE_OFF"}
		return outcome, nil
	}

	// Build request
	builder := NewReviewRequestBuilder(requestID, candidateID, decisionID, r.mode)
	builder.WithCandidate(ReviewCandidate{
		Strategy:    input.StrategyName,
		Side:        input.Side,
		Symbol:      input.Symbol,
		Timeframe:   input.Timeframe,
		EntryType:   input.EntryType,
		EntryPrice:  input.EntryPrice,
		StopLoss:    input.StopLoss,
		TakeProfits: input.TakeProfits,
		RRRatio:     input.RRRatio,
		TAReasoning: input.TAReasoning,
	})
	builder.WithScore(ReviewScore{
		Total:          input.TotalScore,
		Components:     input.ScoreComponents,
		ScoringVersion: input.ScoringVersion,
	})
	builder.WithIndicator(ReviewIndicator{
		Trend15m:      input.Trend15m,
		Trend1h:       input.Trend1h,
		Trend4h:       input.Trend4h,
		RSI14:         input.RSI14,
		RSIState:      input.RSIState,
		MACDHistogram: input.MACDHistogram,
		MACDDirection: input.MACDDirection,
		ATRPct:        input.ATRPct,
		VolumeRatio:   input.VolumeRatio,
		EMAAlignment:  input.EMAAlignment,
	})
	builder.WithRegime(ReviewRegime{
		BTCTrend1h:          input.BTCTrend1h,
		BTCTrend4h:          input.BTCTrend4h,
		BTCFiltersTriggered: input.BTCFiltersTrig,
		BTCDTrend:           input.BTCDTrend,
		BTCDAvailable:       input.BTCDAvailable,
		RelativeStrength4h:  input.RelativeStrength,
		RSClassification:    input.RSClass,
	})
	builder.WithRiskPlan(ReviewRiskPlan{
		Qty:              input.Qty,
		Leverage:         input.Leverage,
		RiskAmountUSD:    input.RiskAmountUSD,
		RiskPct:          input.RiskPct,
		ModifiersApplied: input.ModifiersApplied,
	})

	req := builder.Build()

	// Call LLM
	result, err := r.client.Review(ctx, req)
	if err != nil {
		r.log.Error("LLM review call failed", map[string]any{
			"symbol": input.Symbol,
			"error":  err.Error(),
		})
		return r.applyFallback(outcome, result, "LLM_CALL_ERROR"), nil
	}

	if result == nil {
		return r.applyFallback(outcome, result, "NULL_RESULT"), nil
	}

	outcome.Result = result

	if !result.Called {
		// Budget cap hit or API key missing — fall back per mode
		r.log.Warn("LLM review not called", map[string]any{
			"symbol": input.Symbol,
			"reason": result.InvalidReason,
		})
		return r.applyFallback(outcome, result, result.InvalidReason), nil
	}

	// Validate response
	resp, valErr := ValidateReviewResponse(result.RawResponse, r.mode, &req.Candidate)

	// Log raw response if configured
	if r.cfg.LogRawResponses {
		reqJSON, _ := json.Marshal(req)
		r.log.Info("LLM review request", map[string]any{
			"symbol":   input.Symbol,
			"request":  string(reqJSON),
			"response": result.RawResponse,
			"valid":    valErr == nil,
			"latency_ms": result.LatencyMs,
			"cost_usd": result.CostUSD,
		})
	} else {
		r.log.Info("LLM review decision", map[string]any{
			"symbol":      input.Symbol,
			"valid":       valErr == nil,
			"latency_ms":  result.LatencyMs,
			"cost_usd":    result.CostUSD,
			"tokens_in":   result.TokensIn,
			"tokens_out":  result.TokensOut,
		})
	}

	if valErr != nil {
		// Invalid response
		r.invalidCount++
		r.log.Warn("LLM review invalid response", map[string]any{
			"symbol": input.Symbol,
			"reason": valErr.Error(),
			"raw":    result.RawResponse,
		})
		result.Valid = false
		result.InvalidReason = valErr.Error()
		return r.applyFallback(outcome, result, result.InvalidReason), nil
	}

	// Valid response — apply LLM decision based on mode
	result.Valid = true
	result.Response = resp

	outcome.Confidence = resp.Review.Confidence
	outcome.SetupQuality = resp.Review.SetupQuality
	outcome.ReasonSummary = resp.Review.ReasonSummary
	outcome.SizeMultiplier = resp.RecommendedAdjustment.SizeMultiplier

	switch r.mode {
	case ReviewModeAuditOnly:
		// Audit mode: log but don't affect decision
		r.log.Info("LLM review (audit_only) - LLM would have done", map[string]any{
			"symbol":     input.Symbol,
			"action":     resp.Review.Action,
			"confidence": resp.Review.Confidence,
			"quality":    resp.Review.SetupQuality,
		})
		outcome.Action = ActionApprove
		outcome.SizeMultiplier = 1.0

	case ReviewModeVeto:
		// Veto mode: can REJECT or REDUCE_SIZE
		switch resp.Review.Action {
		case ActionReject:
			outcome.Action = ActionReject
		case ActionReduceSize:
			outcome.Action = ActionReduceSize
		default:
			outcome.Action = ActionApprove
		}

	case ReviewModeReview:
		// Review mode: can REJECT, REDUCE_SIZE, or APPROVE_RETEST_ONLY
		switch resp.Review.Action {
		case ActionReject:
			outcome.Action = ActionReject
		case ActionReduceSize:
			outcome.Action = ActionReduceSize
		case ActionApproveRetestOnly:
			outcome.Action = ActionApproveRetestOnly
		default:
			outcome.Action = ActionApprove
		}
	}

	return outcome, nil
}

func (r *Reviewer) applyFallback(outcome *ReviewOutcome, result *ReviewResult, reason string) *ReviewOutcome {
	if result == nil {
		result = &ReviewResult{Called: false, Valid: false, InvalidReason: reason}
	}
	outcome.Result = result

	switch r.mode {
	case ReviewModeOff:
		outcome.Action = ActionApprove
		outcome.SizeMultiplier = 1.0
	case ReviewModeAuditOnly:
		// In audit_only, invalid/error still doesn't affect decision
		outcome.Action = ActionApprove
		outcome.SizeMultiplier = 1.0
	case ReviewModeVeto:
		// In veto mode, invalid response → REJECT (safest)
		r.log.Warn("LLM review fallback in veto mode: rejecting candidate", map[string]any{
			"reason": reason,
		})
		outcome.Action = ActionReject
		outcome.SizeMultiplier = 1.0
	case ReviewModeReview:
		// In review mode, invalid response → APPROVE (don't punish for our bug)
		r.log.Warn("LLM review fallback in review mode: approving candidate", map[string]any{
			"reason": reason,
		})
		outcome.Action = ActionApprove
		outcome.SizeMultiplier = 1.0
	}

	return outcome
}

// MockReviewClient is a test mock for the review client.
type MockReviewClient struct {
	Result *ReviewResult
	Err    error
	cost   float64
}

func NewMockReviewClient(result *ReviewResult, err error) *MockReviewClient {
	return &MockReviewClient{Result: result, Err: err}
}

func (m *MockReviewClient) Review(_ context.Context, _ ReviewRequest) (*ReviewResult, error) {
	if m.Err != nil {
		return &ReviewResult{
			Valid:         false,
			InvalidReason: "mock_error",
			Called:        true,
		}, m.Err
	}
	m.cost += 0.001
	return m.Result, nil
}

func (m *MockReviewClient) DailyCost() float64    { return m.cost }
func (m *MockReviewClient) ResetDailyCost()         { m.cost = 0 }
