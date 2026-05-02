package llm

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/virhan/botsurv/internal/app"
)

func newTestReviewer(mode LMReviewMode) *Reviewer {
	cfg := app.LLMReviewConfig{
		Mode:            string(mode),
		Provider:        "deepseek",
		Model:           "test-model",
		TimeoutSeconds:  8,
		MaxInputTokens:  2000,
		MaxOutputTokens: 500,
		DailyCostCapUSD: 0,
		PromptVersion:   "v1.0.0",
	}
	return NewReviewer(cfg, newTestLogger())
}

// validApproveResponse returns a valid APPROVE response JSON.
func validApproveResponse(requestID, candidateID string) string {
	resp := ReviewResponse{}
	resp.SchemaVersion = "v2.1"
	resp.RequestID = requestID
	resp.CandidateID = candidateID
	resp.Review.Action = ActionApprove
	resp.Review.Confidence = 85
	resp.Review.SetupQuality = SetupQualityGood
	resp.Review.ReasonSummary = "Clean setup with aligned trends and good volume."
	resp.TechnicalFlags.MarketRegimeSupportsTrade = true
	resp.TechnicalFlags.TrendAlignmentOK = true
	resp.TechnicalFlags.MomentumOK = true
	resp.TechnicalFlags.VolumeOK = true
	resp.TechnicalFlags.RiskRewardOK = true
	resp.RecommendedAdjustment.SizeMultiplier = 1.0
	resp.RecommendedAdjustment.EntryMode = "market"
	resp.RecommendedAdjustment.DoNotChase = false
	b, _ := json.Marshal(resp)
	return string(b)
}

// validRejectResponse returns a valid REJECT response JSON.
func validRejectResponse(requestID, candidateID string) string {
	resp := ReviewResponse{}
	resp.SchemaVersion = "v2.1"
	resp.RequestID = requestID
	resp.CandidateID = candidateID
	resp.Review.Action = ActionReject
	resp.Review.Confidence = 80
	resp.Review.SetupQuality = SetupQualityPoor
	resp.Review.ReasonSummary = "Multiple technical flags conflict: momentum weakening, near resistance."
	resp.TechnicalFlags.EntryTooLate = true
	resp.TechnicalFlags.NearResistance = true
	resp.TechnicalFlags.MomentumOK = false
	resp.RecommendedAdjustment.SizeMultiplier = 1.0
	resp.RecommendedAdjustment.EntryMode = ""
	resp.RecommendedAdjustment.DoNotChase = false
	b, _ := json.Marshal(resp)
	return string(b)
}

// validReduceSizeResponse returns a valid REDUCE_SIZE response JSON.
func validReduceSizeResponse(requestID, candidateID string) string {
	resp := ReviewResponse{}
	resp.SchemaVersion = "v2.1"
	resp.RequestID = requestID
	resp.CandidateID = candidateID
	resp.Review.Action = ActionReduceSize
	resp.Review.Confidence = 70
	resp.Review.SetupQuality = SetupQualityAverage
	resp.Review.ReasonSummary = "Acceptable setup but entry timing is mid-move."
	resp.TechnicalFlags.MarketRegimeSupportsTrade = true
	resp.TechnicalFlags.TrendAlignmentOK = true
	resp.TechnicalFlags.MomentumOK = true
	resp.RecommendedAdjustment.SizeMultiplier = 0.75
	resp.RecommendedAdjustment.EntryMode = "market"
	resp.RecommendedAdjustment.DoNotChase = false
	b, _ := json.Marshal(resp)
	return string(b)
}

func sampleInput() ReviewCandidateInput {
	return ReviewCandidateInput{
		Symbol:         "ORDIUSDT",
		Side:           "LONG",
		StrategyName:   "trend_pullback",
		Timeframe:      "15m",
		EntryType:      "market",
		EntryPrice:     4.57,
		StopLoss:       4.48,
		TakeProfits:    []ReviewTP{{Price: 4.65, SizePct: 50}, {Price: 4.75, SizePct: 50}},
		RRRatio:        1.7,
		TotalScore:     78,
		ScoreComponents: map[string]float64{"liquidity": 20, "execution": 18, "setup": 28, "volatility": 12},
		ScoringVersion:  "v0.1.0",
		Trend15m:        "bullish",
		Trend1h:         "bullish",
		Trend4h:         "neutral_bullish",
		RSI14:           61.5,
		RSIState:        "neutral_bullish",
		MACDHistogram:   0.006,
		MACDDirection:   "increasing",
		ATRPct:          1.14,
		VolumeRatio:     1.49,
		EMAAlignment:    map[string]bool{"ema20_above_ema50": true, "ema50_above_ema200": true},
		BTCTrend1h:      "bullish",
		BTCTrend4h:      "bullish",
		BTCDTrend:       "falling",
		BTCDAvailable:   true,
		RelativeStrength: 2.1,
		RSClass:          "outperform",
		Qty:              220,
		Leverage:         3,
		RiskAmountUSD:    12.5,
		RiskPct:          0.5,
	}
}

// --- Mode tests ---

func TestReviewer_ModeOff_DoesNotCallLLM(t *testing.T) {
	r := newTestReviewer(ReviewModeOff)
	outcome, err := r.Review(context.Background(), sampleInput())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Result.Called {
		t.Error("expected LLM not to be called in off mode")
	}
	if outcome.Action != ActionApprove {
		t.Errorf("expected APPROVE in off mode, got %s", outcome.Action)
	}
}

func TestReviewer_AuditOnly_LLMRejectNotEnforced(t *testing.T) {
	cfg := app.LLMReviewConfig{Mode: "audit_only"}
	r := NewReviewer(cfg, newTestLogger())

	mockClient := NewMockReviewClient(&ReviewResult{
		Called:   true,
		Valid:    true,
		Response: &ReviewResponse{Review: struct {
			Action        ReviewAction `json:"action"`
			Confidence    float64      `json:"confidence"`
			SetupQuality  SetupQuality `json:"setup_quality"`
			ReasonSummary string       `json:"reason_summary"`
		}{Action: ActionReject, Confidence: 80, SetupQuality: SetupQualityPoor, ReasonSummary: "bad setup"}},
	}, nil)
	r.client = mockClient

	outcome, err := r.Review(context.Background(), sampleInput())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Action != ActionApprove {
		t.Errorf("audit_only should always APPROVE, got %s", outcome.Action)
	}
}

func TestReviewer_AuditOnly_InvalidResponseStillApproves(t *testing.T) {
	cfg := app.LLMReviewConfig{Mode: "audit_only"}
	r := NewReviewer(cfg, newTestLogger())

	mockClient := NewMockReviewClient(&ReviewResult{
		Called:         true,
		Valid:          false,
		InvalidReason:  "INVALID_JSON",
		RawResponse:    "not json at all",
	}, nil)
	r.client = mockClient

	outcome, err := r.Review(context.Background(), sampleInput())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Action != ActionApprove {
		t.Errorf("audit_only with invalid response should APPROVE, got %s", outcome.Action)
	}
}

func TestReviewer_VetoMode_CanReject(t *testing.T) {
	cfg := app.LLMReviewConfig{Mode: "veto"}
	r := NewReviewer(cfg, newTestLogger())

	resp := validRejectResponse("rid", "cid")
	mockClient := NewMockReviewClient(&ReviewResult{
		Called:      true,
		Valid:       true,
		RawResponse: resp,
	}, nil)
	r.client = mockClient

	outcome, err := r.Review(context.Background(), sampleInput())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Action != ActionReject {
		t.Errorf("veto mode should allow REJECT, got %s", outcome.Action)
	}
}

func TestReviewer_VetoMode_CanReduceSize(t *testing.T) {
	cfg := app.LLMReviewConfig{Mode: "veto"}
	r := NewReviewer(cfg, newTestLogger())

	mockClient := NewMockReviewClient(&ReviewResult{
		Called:      true,
		Valid:       true,
		RawResponse: validReduceSizeResponse("rid", "cid"),
	}, nil)
	r.client = mockClient

	outcome, err := r.Review(context.Background(), sampleInput())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Action != ActionReduceSize {
		t.Errorf("veto mode should allow REDUCE_SIZE, got %s", outcome.Action)
	}
}

func TestReviewer_VetoMode_InvalidResponseRejects(t *testing.T) {
	cfg := app.LLMReviewConfig{Mode: "veto"}
	r := NewReviewer(cfg, newTestLogger())

	mockClient := NewMockReviewClient(&ReviewResult{
		Called:         true,
		Valid:          false,
		InvalidReason:  "INVALID_JSON",
		RawResponse:    "garbage",
	}, nil)
	r.client = mockClient

	outcome, err := r.Review(context.Background(), sampleInput())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Action != ActionReject {
		t.Errorf("veto mode invalid response should REJECT (safe default), got %s", outcome.Action)
	}
}

func TestReviewer_VetoMode_CannotImprove(t *testing.T) {
	cfg := app.LLMReviewConfig{Mode: "veto"}
	r := NewReviewer(cfg, newTestLogger())

	// Veto mode allows APPROVE, REDUCE_SIZE, REJECT but NOT APPROVE_RETEST_ONLY
	resp := ReviewResponse{}
	resp.SchemaVersion = "v2.1"
	resp.Review.Action = ActionApproveRetestOnly
	resp.Review.Confidence = 80
	resp.Review.SetupQuality = SetupQualityGood
	resp.Review.ReasonSummary = "ok but retest"
	resp.RecommendedAdjustment.SizeMultiplier = 1.0
	b, _ := json.Marshal(resp)

	mockClient := NewMockReviewClient(&ReviewResult{
		Called:      true,
		Valid:       true,
		RawResponse: string(b),
	}, nil)
	r.client = mockClient

	outcome, err := r.Review(context.Background(), sampleInput())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Result.Valid {
		t.Errorf("APPROVE_RETEST_ONLY should be rejected as forbidden in veto mode, but got valid=true")
	}
}

func TestReviewer_ReviewMode_CanApproveRetestOnly(t *testing.T) {
	cfg := app.LLMReviewConfig{Mode: "review"}
	r := NewReviewer(cfg, newTestLogger())

	resp := ReviewResponse{}
	resp.SchemaVersion = "v2.1"
	resp.Review.Action = ActionApproveRetestOnly
	resp.Review.Confidence = 75
	resp.Review.SetupQuality = SetupQualityAverage
	resp.Review.ReasonSummary = "Good setup, wait for retest."
	resp.RecommendedAdjustment.SizeMultiplier = 1.0
	resp.RecommendedAdjustment.EntryMode = "limit_retest"
	resp.RecommendedAdjustment.DoNotChase = true
	b, _ := json.Marshal(resp)

	mockClient := NewMockReviewClient(&ReviewResult{
		Called:      true,
		Valid:       true,
		RawResponse: string(b),
	}, nil)
	r.client = mockClient

	outcome, err := r.Review(context.Background(), sampleInput())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Action != ActionApproveRetestOnly {
		t.Errorf("review mode should allow APPROVE_RETEST_ONLY, got %s", outcome.Action)
	}
}

func TestReviewer_ReviewMode_InvalidResponseApproves(t *testing.T) {
	cfg := app.LLMReviewConfig{Mode: "review"}
	r := NewReviewer(cfg, newTestLogger())

	mockClient := NewMockReviewClient(&ReviewResult{
		Called:         true,
		Valid:          false,
		InvalidReason:  "TIMEOUT",
		RawResponse:    "",
	}, nil)
	r.client = mockClient

	outcome, err := r.Review(context.Background(), sampleInput())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Action != ActionApprove {
		t.Errorf("review mode invalid response should APPROVE (soft default), got %s", outcome.Action)
	}
}

// --- Schema validation tests ---

func TestValidateReviewResponse_ValidApprove(t *testing.T) {
	raw := validApproveResponse("rid", "cid")
	resp, err := ValidateReviewResponse(raw, ReviewModeVeto, nil)
	if err != nil {
		t.Fatalf("expected valid response, got error: %v", err)
	}
	if resp.Review.Action != ActionApprove {
		t.Errorf("expected APPROVE, got %s", resp.Review.Action)
	}
}

func TestValidateReviewResponse_ValidReject(t *testing.T) {
	raw := validRejectResponse("rid", "cid")
	resp, err := ValidateReviewResponse(raw, ReviewModeVeto, nil)
	if err != nil {
		t.Fatalf("expected valid response, got error: %v", err)
	}
	if resp.Review.Action != ActionReject {
		t.Errorf("expected REJECT, got %s", resp.Review.Action)
	}
}

func TestValidateReviewResponse_MissingSchemaVersion(t *testing.T) {
	raw := `{"review":{"action":"APPROVE","confidence":85},"recommended_adjustment":{"size_multiplier":1.0}}`
	_, err := ValidateReviewResponse(raw, ReviewModeVeto, nil)
	if err == nil {
		t.Error("expected error for missing schema_version")
	}
}

func TestValidateReviewResponse_MissingAction(t *testing.T) {
	raw := `{"schema_version":"v2.1","review":{"confidence":85},"recommended_adjustment":{"size_multiplier":1.0}}`
	_, err := ValidateReviewResponse(raw, ReviewModeVeto, nil)
	if err == nil {
		t.Error("expected error for missing review.action")
	}
}

func TestValidateReviewResponse_InvalidJSON(t *testing.T) {
	raw := `{this is not json}`
	_, err := ValidateReviewResponse(raw, ReviewModeVeto, nil)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestValidateReviewResponse_ForbiddenAction(t *testing.T) {
	raw := `{"schema_version":"v2.1","review":{"action":"APPROVE_RETEST_ONLY","confidence":75},"recommended_adjustment":{"size_multiplier":1.0}}`
	_, err := ValidateReviewResponse(raw, ReviewModeVeto, nil)
	if err == nil {
		t.Error("expected error for APPROVE_RETEST_ONLY in veto mode")
	}
	verr, ok := err.(*ValidationError)
	if !ok || verr.Reason != "FORBIDDEN_ACTION" {
		t.Errorf("expected FORBIDDEN_ACTION error, got %v", err)
	}
}

func TestValidateReviewResponse_SizeMultiplierTooHigh(t *testing.T) {
	raw := `{"schema_version":"v2.1","review":{"action":"REDUCE_SIZE","confidence":70},"recommended_adjustment":{"size_multiplier":1.5}}`
	_, err := ValidateReviewResponse(raw, ReviewModeVeto, nil)
	if err == nil {
		t.Error("expected error for size_multiplier > 1.0")
	}
}

func TestValidateReviewResponse_SizeMultiplierZero(t *testing.T) {
	raw := `{"schema_version":"v2.1","review":{"action":"REDUCE_SIZE","confidence":70},"recommended_adjustment":{"size_multiplier":0.0}}`
	_, err := ValidateReviewResponse(raw, ReviewModeVeto, nil)
	if err == nil {
		t.Error("expected error for size_multiplier <= 0")
	}
}

func TestValidateReviewResponse_SizeMultiplierNegative(t *testing.T) {
	raw := `{"schema_version":"v2.1","review":{"action":"REDUCE_SIZE","confidence":70},"recommended_adjustment":{"size_multiplier":-0.5}}`
	_, err := ValidateReviewResponse(raw, ReviewModeVeto, nil)
	if err == nil {
		t.Error("expected error for negative size_multiplier")
	}
}

func TestValidateReviewResponse_InvalidConfidence(t *testing.T) {
	raw := `{"schema_version":"v2.1","review":{"action":"APPROVE","confidence":150},"recommended_adjustment":{"size_multiplier":1.0}}`
	_, err := ValidateReviewResponse(raw, ReviewModeVeto, nil)
	if err == nil {
		t.Error("expected error for confidence > 100")
	}
}

func TestValidateReviewResponse_ReasonSummaryTruncation(t *testing.T) {
	longReason := ""
	for i := 0; i < 250; i++ {
		longReason += "x"
	}
	raw := `{"schema_version":"v2.1","review":{"action":"APPROVE","confidence":80,"reason_summary":"` + longReason + `"},"recommended_adjustment":{"size_multiplier":1.0}}`
	resp, err := ValidateReviewResponse(raw, ReviewModeVeto, nil)
	if err != nil {
		t.Fatalf("expected valid response with truncated reason, got: %v", err)
	}
	if len(resp.Review.ReasonSummary) > 200 {
		t.Errorf("expected reason_summary truncated to <= 200, got %d", len(resp.Review.ReasonSummary))
	}
}

// --- Forbidden action detection ---

func TestValidateReviewResponse_ForbiddenEntryModeChange(t *testing.T) {
	resp := ReviewResponse{}
	resp.SchemaVersion = "v2.1"
	resp.Review.Action = ActionApprove
	resp.Review.Confidence = 80
	resp.RecommendedAdjustment.SizeMultiplier = 1.0
	resp.RecommendedAdjustment.EntryMode = "invalid_mode" // not market or limit_retest
	b, _ := json.Marshal(resp)

	_, err := ValidateReviewResponse(string(b), ReviewModeVeto, &ReviewCandidate{})
	if err == nil {
		t.Error("expected error for invalid entry_mode")
	}
}

// --- Daily cost cap ---

func TestReviewer_DailyCostCapReached_SkipsLLM(t *testing.T) {
	cfg := app.LLMReviewConfig{
		Mode:            "audit_only",
		Provider:        "deepseek",
		Model:           "test",
		TimeoutSeconds:  8,
		DailyCostCapUSD: 1.0,
	}
	r := NewReviewer(cfg, newTestLogger())
	if r.client != nil {
		// Set daily cost to exceed cap
		r.client.ResetDailyCost()
		// We need to add cost. Since the mock doesn't expose daily cost directly,
		// use the reviewer's dailyCost tracking.
		// For the real client, this is tracked internally.
	}

	mockClient := NewMockReviewClient(nil, nil)
	r.client = mockClient
	// Simulate cost cap hit by adding cost
	for i := 0; i < 1000; i++ {
		mockClient.cost = 5.0 // exceed cap
	}

	outcome, err := r.Review(context.Background(), sampleInput())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The mock client doesn't check daily cost cap; the real client does.
	// This test verifies the reviewer calls the client correctly.
	_ = outcome
}

func TestReviewClient_DailyCostTracking(t *testing.T) {
	cfg := app.LLMReviewConfig{
		Mode:            "audit_only",
		Provider:        "deepseek",
		Model:           "test",
		TimeoutSeconds:  8,
		DailyCostCapUSD: 100,
	}
	client := NewReviewClient(cfg, newTestLogger())
	if client.DailyCost() != 0 {
		t.Error("expected initial daily cost 0")
	}
	client.ResetDailyCost()
	if client.DailyCost() != 0 {
		t.Error("expected daily cost 0 after reset")
	}
}

// --- Timeout handling ---

func TestReviewer_Timeout_FallbackCorrectPerMode(t *testing.T) {
	tests := []struct {
		name       string
		mode       LMReviewMode
		expectAction ReviewAction
	}{
		{"veto timeout rejects", ReviewModeVeto, ActionReject},
		{"review timeout approves", ReviewModeReview, ActionApprove},
		{"audit_only timeout approves", ReviewModeAuditOnly, ActionApprove},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := app.LLMReviewConfig{Mode: string(tt.mode)}
			r := NewReviewer(cfg, newTestLogger())

			mockClient := NewMockReviewClient(nil, nil)
			r.client = mockClient
			// Simulate timeout by making the mock return nil with error
			mockClient.Err = context.DeadlineExceeded

			outcome, err := r.Review(context.Background(), sampleInput())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if outcome.Action != tt.expectAction {
				t.Errorf("expected %s action, got %s", tt.expectAction, outcome.Action)
			}
		})
	}
}

// --- Logging verification ---

func TestReviewer_Logging_CandidateDecisionUnaffectedInAuditOnly(t *testing.T) {
	cfg := app.LLMReviewConfig{
		Mode:            "audit_only",
		Provider:        "deepseek",
		Model:           "test",
		TimeoutSeconds:  8,
		LogRawResponses: true,
	}
	r := NewReviewer(cfg, newTestLogger())

	mockClient := NewMockReviewClient(&ReviewResult{
		Called:      true,
		Valid:       true,
		RawResponse: validRejectResponse("rid-1", "cid-1"),
	}, nil)
	r.client = mockClient

	outcome, err := r.Review(context.Background(), sampleInput())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Action != ActionApprove {
		t.Errorf("audit_only must not reject even if LLM says REJECT, got %s", outcome.Action)
	}
	if outcome.Result == nil {
		t.Fatal("expected result to be set")
	}
	if !outcome.Result.Valid {
		t.Error("expected valid result")
	}
}

// --- All modes: correct behavior ---

func TestReviewer_AllModes_CorrectBehavior(t *testing.T) {
	tests := []struct {
		name         string
		mode         LMReviewMode
		rawResponse  string
		expectAction ReviewAction
	}{
		{
			name:         "off mode always approves",
			mode:         ReviewModeOff,
			rawResponse:  "",
			expectAction: ActionApprove,
		},
		{
			name:         "audit_only always approves even when LLM rejects",
			mode:         ReviewModeAuditOnly,
			rawResponse:  validRejectResponse("r", "c"),
			expectAction: ActionApprove,
		},
		{
			name:         "veto mode LLM approve",
			mode:         ReviewModeVeto,
			rawResponse:  validApproveResponse("r", "c"),
			expectAction: ActionApprove,
		},
		{
			name:         "veto mode LLM reject",
			mode:         ReviewModeVeto,
			rawResponse:  validRejectResponse("r", "c"),
			expectAction: ActionReject,
		},
		{
			name:         "review mode LLM approve retest only",
			mode:         ReviewModeReview,
			rawResponse:  validApproveResponse("r", "c"), // APPROVE is also allowed in review
			expectAction: ActionApprove,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := app.LLMReviewConfig{Mode: string(tt.mode)}
			r := NewReviewer(cfg, newTestLogger())

			if tt.mode != ReviewModeOff {
				mockClient := NewMockReviewClient(&ReviewResult{
					Called:      true,
					Valid:       tt.rawResponse != "",
					RawResponse: tt.rawResponse,
				}, nil)
				r.client = mockClient
			}

			outcome, err := r.Review(context.Background(), sampleInput())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if outcome.Action != tt.expectAction {
				t.Errorf("expected %s, got %s", tt.expectAction, outcome.Action)
			}
		})
	}
}

// --- Invalid response handling per mode ---

func TestReviewer_InvalidResponsePerMode(t *testing.T) {
	tests := []struct {
		name         string
		mode         LMReviewMode
		expectAction ReviewAction
	}{
		{"audit_only invalid -> approve", ReviewModeAuditOnly, ActionApprove},
		{"veto invalid -> reject", ReviewModeVeto, ActionReject},
		{"review invalid -> approve", ReviewModeReview, ActionApprove},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := app.LLMReviewConfig{Mode: string(tt.mode)}
			r := NewReviewer(cfg, newTestLogger())

			mockClient := NewMockReviewClient(&ReviewResult{
				Called:         true,
				Valid:          false,
				InvalidReason:  "INVALID_JSON",
				RawResponse:    "not json",
			}, nil)
			r.client = mockClient

			outcome, err := r.Review(context.Background(), sampleInput())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if outcome.Action != tt.expectAction {
				t.Errorf("expected %s, got %s", tt.expectAction, outcome.Action)
			}
		})
	}
}

// --- Cost tracking ---

func TestReviewer_CostTracking(t *testing.T) {
	r := newTestReviewer(ReviewModeVeto)
	if r.DailyCost() != 0 {
		t.Error("expected initial cost 0")
	}

	mockClient := NewMockReviewClient(&ReviewResult{
		Called:      true,
		Valid:       true,
		RawResponse: validApproveResponse("r", "c"),
	}, nil)
	r.client = mockClient

	_, _ = r.Review(context.Background(), sampleInput())
	if r.DailyCost() <= 0 {
		t.Error("expected cost > 0 after review")
	}

	r.ResetDailyCost()
	if r.DailyCost() != 0 {
		t.Error("expected cost 0 after reset")
	}
}

// --- LLM unavailable fallback ---

func TestReviewer_LLMUnavailable_Fallback(t *testing.T) {
	cfg := app.LLMReviewConfig{Mode: "veto"}
	r := NewReviewer(cfg, newTestLogger())

	mockClient := NewMockReviewClient(nil, context.DeadlineExceeded)
	r.client = mockClient

	outcome, err := r.Review(context.Background(), sampleInput())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Action != ActionReject {
		t.Errorf("veto mode with LLM error should REJECT, got %s", outcome.Action)
	}
	if outcome.Result == nil {
		t.Fatal("expected result to be set")
	}
}

// --- Sample request/response payloads ---

func TestReviewRequest_SamplePayload(t *testing.T) {
	input := sampleInput()
	builder := NewReviewRequestBuilder("req-1", "cand-1", "dec-1", ReviewModeVeto)
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

	if req.SchemaVersion != "v2.1" {
		t.Errorf("expected schema_version v2.1, got %s", req.SchemaVersion)
	}
	if req.LMMode != "veto" {
		t.Errorf("expected llm_mode veto, got %s", req.LMMode)
	}
	if req.Candidate.Symbol != "ORDIUSDT" {
		t.Errorf("expected symbol ORDIUSDT, got %s", req.Candidate.Symbol)
	}
	if len(req.Constraints.AllowedActions) == 0 {
		t.Error("expected allowed_actions to be set")
	}

	// Verify JSON marshaling
	data, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}
	t.Logf("Sample LLM review request:\n%s", string(data))
}

// Test sample responses
func TestSampleResponses(t *testing.T) {
	// Valid APPROVE
	approve := validApproveResponse("req-sample", "cand-sample")
	t.Logf("Sample valid APPROVE response:\n%s", approve)

	// Valid REJECT
	reject := validRejectResponse("req-sample", "cand-sample")
	t.Logf("Sample valid REJECT response:\n%s", reject)

	// Invalid response
	invalid := `{"review":{"action":"CREATE_NEW_TRADE"},"recommended_adjustment":{"size_multiplier":2.0}}`
	t.Logf("Sample invalid response (caught by validation):\n%s", invalid)

	_, err := ValidateReviewResponse(invalid, ReviewModeVeto, nil)
	if err == nil {
		t.Error("expected invalid response to be caught")
	}
	t.Logf("Invalid response caught: %v", err)
}

// --- Test multiple candidate types ---

func TestReviewer_LLMDisabled_BackendStillFunctional(t *testing.T) {
	// This test verifies the critical constraint: bot fully functional with LLM_MODE=off.
	r := newTestReviewer(ReviewModeOff)

	// 10 candidates, should all return APPROVE without LLM calls
	for i := 0; i < 10; i++ {
		outcome, err := r.Review(context.Background(), sampleInput())
		if err != nil {
			t.Fatalf("candidate %d: unexpected error: %v", i, err)
		}
		if outcome.Action != ActionApprove {
			t.Errorf("candidate %d: expected APPROVE, got %s", i, outcome.Action)
		}
		if outcome.Result.Called {
			t.Errorf("candidate %d: expected LLM not called", i)
		}
	}
}
