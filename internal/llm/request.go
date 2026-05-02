package llm

import "time"

// LMReviewMode is the operational mode for the LLM Reviewer.
type LMReviewMode string

const (
	ReviewModeOff        LMReviewMode = "off"
	ReviewModeAuditOnly  LMReviewMode = "audit_only"
	ReviewModeVeto       LMReviewMode = "veto"
	ReviewModeReview     LMReviewMode = "review"
)

// ValidReviewModes returns the set of valid review modes.
var ValidReviewModes = map[LMReviewMode]bool{
	ReviewModeOff:       true,
	ReviewModeAuditOnly: true,
	ReviewModeVeto:      true,
	ReviewModeReview:    true,
}

// AllowedActionsForMode returns the allowed LLM actions for a given mode.
func AllowedActionsForMode(mode LMReviewMode) []string {
	switch mode {
	case ReviewModeAuditOnly:
		return []string{"APPROVE", "APPROVE_RETEST_ONLY", "REDUCE_SIZE", "REJECT"}
	case ReviewModeVeto:
		return []string{"APPROVE", "REDUCE_SIZE", "REJECT"}
	case ReviewModeReview:
		return []string{"APPROVE", "APPROVE_RETEST_ONLY", "REDUCE_SIZE", "REJECT"}
	default:
		return nil
	}
}

// ReviewRequest is the structured request sent to the LLM for review.
type ReviewRequest struct {
	SchemaVersion string              `json:"schema_version"`
	RequestID     string              `json:"request_id"`
	CandidateID   string              `json:"candidate_id"`
	DecisionID    string              `json:"decision_id"`
	Timestamp     string              `json:"timestamp"`
	LMMode       string              `json:"llm_mode"`
	Candidate     ReviewCandidate     `json:"candidate"`
	Score         ReviewScore         `json:"score"`
	Indicator     ReviewIndicator     `json:"indicator_summary"`
	Regime        ReviewRegime        `json:"regime_summary"`
	RiskPlan      ReviewRiskPlan      `json:"risk_plan"`
	Constraints   ReviewConstraints   `json:"constraints"`
}

// ReviewCandidate contains the candidate trade details.
type ReviewCandidate struct {
	Strategy     string           `json:"strategy"`
	Side         string           `json:"side"`
	Symbol       string           `json:"symbol"`
	Timeframe    string           `json:"timeframe"`
	EntryType    string           `json:"entry_type"`
	EntryPrice   float64          `json:"entry_price"`
	StopLoss     float64          `json:"stop_loss"`
	TakeProfits  []ReviewTP       `json:"take_profits"`
	RRRatio      float64          `json:"risk_reward_ratio"`
	TAReasoning  interface{}      `json:"ta_reasoning"`
}

// ReviewTP is a take-profit level.
type ReviewTP struct {
	Price   float64 `json:"price"`
	SizePct float64 `json:"size_pct"`
}

// ReviewScore contains the candidate scoring breakdown.
type ReviewScore struct {
	Total           float64                `json:"total"`
	Components      map[string]float64     `json:"components"`
	ScoringVersion  string                 `json:"scoring_version"`
}

// ReviewIndicator contains pre-computed indicator values.
type ReviewIndicator struct {
	Trend15m       string                 `json:"trend_15m"`
	Trend1h        string                 `json:"trend_1h"`
	Trend4h        string                 `json:"trend_4h"`
	RSI14          float64                `json:"rsi_14"`
	RSIState       string                 `json:"rsi_state"`
	MACDHistogram  float64                `json:"macd_histogram"`
	MACDDirection  string                 `json:"macd_direction"`
	ATRPct         float64                `json:"atr_pct"`
	VolumeRatio    float64                `json:"volume_ratio"`
	EMAAlignment   map[string]bool        `json:"ema_alignment"`
}

// ReviewRegime contains market regime context.
type ReviewRegime struct {
	BTCTrend1h           string   `json:"btc_trend_1h"`
	BTCTrend4h           string   `json:"btc_trend_4h"`
	BTCFiltersTriggered  []string `json:"btc_filters_triggered"`
	BTCDTrend            string   `json:"btcd_trend"`
	BTCDAvailable        bool     `json:"btcd_available"`
	RelativeStrength4h   float64  `json:"relative_strength_4h"`
	RSClassification     string   `json:"rs_classification"`
}

// ReviewRiskPlan contains the computed risk plan.
type ReviewRiskPlan struct {
	Qty             float64  `json:"qty"`
	Leverage        float64  `json:"leverage"`
	RiskAmountUSD   float64  `json:"risk_amount_usd"`
	RiskPct         float64  `json:"risk_pct"`
	ModifiersApplied []string `json:"modifiers_applied"`
}

// ReviewConstraints defines what the LLM may do.
type ReviewConstraints struct {
	AllowedActions      []string `json:"allowed_actions"`
	MustReturnJSONOnly  bool     `json:"must_return_json_only"`
}

// ReviewRequestBuilder builds a ReviewRequest from pipeline data.
type ReviewRequestBuilder struct {
	req ReviewRequest
}

// NewReviewRequestBuilder creates a builder with defaults.
func NewReviewRequestBuilder(requestID, candidateID, decisionID string, mode LMReviewMode) *ReviewRequestBuilder {
	return &ReviewRequestBuilder{
		req: ReviewRequest{
			SchemaVersion: "v2.1",
			RequestID:     requestID,
			CandidateID:   candidateID,
			DecisionID:    decisionID,
			Timestamp:     time.Now().UTC().Format(time.RFC3339),
			LMMode:       string(mode),
			Constraints: ReviewConstraints{
				AllowedActions:     AllowedActionsForMode(mode),
				MustReturnJSONOnly: true,
			},
		},
	}
}

func (b *ReviewRequestBuilder) WithCandidate(c ReviewCandidate) *ReviewRequestBuilder {
	b.req.Candidate = c
	return b
}

func (b *ReviewRequestBuilder) WithScore(s ReviewScore) *ReviewRequestBuilder {
	b.req.Score = s
	return b
}

func (b *ReviewRequestBuilder) WithIndicator(i ReviewIndicator) *ReviewRequestBuilder {
	b.req.Indicator = i
	return b
}

func (b *ReviewRequestBuilder) WithRegime(r ReviewRegime) *ReviewRequestBuilder {
	b.req.Regime = r
	return b
}

func (b *ReviewRequestBuilder) WithRiskPlan(r ReviewRiskPlan) *ReviewRequestBuilder {
	b.req.RiskPlan = r
	return b
}

func (b *ReviewRequestBuilder) Build() ReviewRequest {
	return b.req
}
