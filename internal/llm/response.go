package llm

// ReviewAction is the action the LLM decided.
type ReviewAction string

const (
	ActionApprove          ReviewAction = "APPROVE"
	ActionApproveRetestOnly ReviewAction = "APPROVE_RETEST_ONLY"
	ActionReduceSize       ReviewAction = "REDUCE_SIZE"
	ActionReject           ReviewAction = "REJECT"
)

// ValidReviewActions is the set of valid review actions.
var ValidReviewActions = map[ReviewAction]bool{
	ActionApprove:           true,
	ActionApproveRetestOnly: true,
	ActionReduceSize:        true,
	ActionReject:            true,
}

// SetupQuality is the LLM's assessment of setup quality.
type SetupQuality string

const (
	SetupQualityExcellent SetupQuality = "excellent"
	SetupQualityGood      SetupQuality = "good"
	SetupQualityAverage   SetupQuality = "average"
	SetupQualityPoor      SetupQuality = "poor"
)

// ReviewResponse is the strict JSON response expected from the LLM.
type ReviewResponse struct {
	SchemaVersion string `json:"schema_version"`
	RequestID     string `json:"request_id"`
	CandidateID   string `json:"candidate_id"`

	Review struct {
		Action         ReviewAction `json:"action"`
		Confidence     float64      `json:"confidence"`
		SetupQuality   SetupQuality `json:"setup_quality"`
		ReasonSummary  string       `json:"reason_summary"`
	} `json:"review"`

	TechnicalFlags struct {
		MarketRegimeSupportsTrade bool `json:"market_regime_supports_trade"`
		BTCConflict               bool `json:"btc_conflict"`
		BTCDConflict              bool `json:"btcd_conflict"`
		TargetOutperformingBTC    bool `json:"target_outperforming_btc"`
		TrendAlignmentOK          bool `json:"trend_alignment_ok"`
		MomentumOK                bool `json:"momentum_ok"`
		VolumeOK                  bool `json:"volume_ok"`
		EntryTooLate              bool `json:"entry_too_late"`
		NearResistance            bool `json:"near_resistance"`
		NearSupport               bool `json:"near_support"`
		RiskRewardOK              bool `json:"risk_reward_ok"`
	} `json:"technical_flags"`

	RecommendedAdjustment struct {
		SizeMultiplier float64 `json:"size_multiplier"`
		EntryMode      string  `json:"entry_mode"`
		DoNotChase     bool    `json:"do_not_chase"`
	} `json:"recommended_adjustment"`

	RejectionReason *string `json:"rejection_reason"`
}

// ReviewResult is the processed result from an LLM review call.
type ReviewResult struct {
	// Original response (raw JSON)
	RawResponse string
	// Parsed response (may be nil if invalid)
	Response *ReviewResponse
	// Effective action after mode-based fallback
	EffectiveAction ReviewAction
	// Effective size multiplier after validation
	EffectiveSizeMultiplier float64
	// Whether the LLM call succeeded and produced a valid response
	Valid bool
	// Reason for invalidation if !Valid
	InvalidReason string
	// Cost tracking
	TokensIn     int
	TokensOut    int
	LatencyMs    int64
	CostUSD      float64
	// Whether the LLM was actually called (false if mode=off or cap hit)
	Called bool
}

// ReviewOutcome is the final outcome of a review against a candidate.
type ReviewOutcome struct {
	CandidateID   string
	Action        ReviewAction
	SizeMultiplier float64
	ReasonSummary string
	Confidence    float64
	SetupQuality  SetupQuality
	Result        *ReviewResult
}
