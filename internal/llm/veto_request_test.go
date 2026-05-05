package llm

import (
	"encoding/json"
	"testing"
)

func TestBuildVetoContext_Basic(t *testing.T) {
	req := VetoRequestInput{
		RequestID:    "req-1",
		CandidateID:  "cand-1",
		Symbol:       "BTCUSDT",
		Side:         "LONG",
		Strategy:     "trend_pullback",
		EntryPrice:   65000,
		StopLoss:     64800,
		TakeProfit:   65500,
		RR:           2.0,
		Score:        72.5,
		SetupQuality: 17,
		Regime:       "trend_up",
		BTCTrend:     "bullish",
		VolumeRatio:  1.3,
		ATRPct:       0.5,
	}
	json := BuildVetoContext(req)
	if json == "" {
		t.Fatal("expected non-empty JSON")
	}
	if len(json) < 50 {
		t.Fatalf("JSON too short: %s", json)
	}
}

func TestBuildVetoContext_IncludesFlags(t *testing.T) {
	req := VetoRequestInput{
		Symbol:         "ETHUSDT",
		Side:           "SHORT",
		Strategy:       "breakout_retest",
		EntryPrice:     3000,
		StopLoss:       3100,
		TakeProfit:     2900,
		RR:             2.5,
		Score:          68,
		AmbiguityFlags: []string{"mixed_timeframe", "volume_not_strong"},
		Warnings:       []string{"btc_near_resistance"},
	}
	json := BuildVetoContext(req)
	if json == "" {
		t.Fatal("expected non-empty JSON")
	}
}

func TestParseVetoResponse_ValidJSON(t *testing.T) {
	raw := `{"schema_version":"2.1","request_id":"r1","candidate_id":"c1","review":{"action":"APPROVE","confidence":85,"setup_quality":"good","reason_summary":"strong setup"},"technical_flags":{"market_regime_supports_trade":true},"recommended_adjustment":{"size_multiplier":1.0,"entry_mode":"market","do_not_chase":false},"rejection_reason":null}`
	dec, err := ParseVetoResponse(raw)
	if err != nil {
		t.Fatalf("ParseVetoResponse: %v", err)
	}
	if dec.Decision != "ALLOW_MARKET" {
		t.Errorf("expected ALLOW_MARKET, got %s", dec.Decision)
	}
}

func TestParseVetoResponse_InvalidJSON(t *testing.T) {
	_, err := ParseVetoResponse("not json")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseVetoResponse_RejectRequiresReason(t *testing.T) {
	raw := `{"schema_version":"2.1","review":{"action":"REJECT","confidence":90,"setup_quality":"poor","reason_summary":"bad"},"recommended_adjustment":{"size_multiplier":1.0},"rejection_reason":null}`
	dec, err := ParseVetoResponse(raw)
	if err == nil {
		t.Fatal("expected error when REJECT without rejection_reason")
	}
	_ = dec
}

func TestParseVetoResponse_ConfidenceClamped(t *testing.T) {
	raw := `{"schema_version":"2.1","review":{"action":"APPROVE","confidence":0.85,"setup_quality":"good","reason_summary":"ok"},"recommended_adjustment":{"size_multiplier":1.0},"rejection_reason":null}`
	dec, err := ParseVetoResponse(raw)
	if err != nil {
		t.Fatalf("ParseVetoResponse: %v", err)
	}
	// 0.85 is already < 1.0, should stay as-is
	if dec.Confidence != 0.85 {
		t.Errorf("expected confidence 0.85, got %f", dec.Confidence)
	}
}

func TestParseVetoResponse_ReduceSize(t *testing.T) {
	raw := `{"schema_version":"2.1","review":{"action":"REDUCE_SIZE","confidence":78,"setup_quality":"average","reason_summary":"low liquidity"},"recommended_adjustment":{"size_multiplier":0.5,"entry_mode":"limit_retest"},"rejection_reason":null}`
	dec, err := ParseVetoResponse(raw)
	if err != nil {
		t.Fatalf("ParseVetoResponse: %v", err)
	}
	if dec.Decision != "REDUCE_SIZE" {
		t.Errorf("expected REDUCE_SIZE, got %s", dec.Decision)
	}
}

func TestParseVetoResponse_ApproveRetestOnly(t *testing.T) {
	raw := `{"schema_version":"2.1","review":{"action":"APPROVE_RETEST_ONLY","confidence":75,"setup_quality":"good","reason_summary":"near resistance"},"recommended_adjustment":{"size_multiplier":1.0,"entry_mode":"limit_retest"},"rejection_reason":null}`
	dec, err := ParseVetoResponse(raw)
	if err != nil {
		t.Fatalf("ParseVetoResponse: %v", err)
	}
	if dec.Decision != "ALLOW_LIMIT_RETEST" {
		t.Errorf("expected ALLOW_LIMIT_RETEST, got %s", dec.Decision)
	}
}

func TestBuildVetoContext_QuotesInWarnings_ValidJSON(t *testing.T) {
	req := VetoRequestInput{
		Symbol:         "BTCUSDT",
		Side:           "LONG",
		Strategy:       "breakout_retest",
		EntryPrice:     65000,
		StopLoss:       64800,
		Warnings:       []string{`btc "flash" crash`, `don't enter`},
		AmbiguityFlags: []string{`"trend" unclear`},
	}
	raw := BuildVetoContext(req)
	if raw == "" {
		t.Fatal("expected non-empty JSON")
	}
	// Verify it's valid JSON
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("BuildVetoContext produced invalid JSON: %v\nraw: %s", err, raw)
	}
	// Verify warnings contain the quoted strings
	if _, ok := m["warnings"]; !ok {
		t.Fatal("expected warnings key in output")
	}
}

func TestParseVetoResponse_ConfidenceBelowMinReturnsBlock(t *testing.T) {
	// confidence 0.59 < 0.75 should BLOCK
	raw := `{"schema_version":"2.1","review":{"action":"APPROVE","confidence":0.59,"setup_quality":"good","reason_summary":"ok"},"recommended_adjustment":{"size_multiplier":1.0},"rejection_reason":null}`
	dec, err := ParseVetoResponse(raw)
	if err == nil {
		t.Fatal("expected error for confidence below 0.75")
	}
	if dec.Decision != "BLOCK" {
		t.Errorf("expected BLOCK for low confidence, got %s", dec.Decision)
	}
}

func TestParseVetoResponse_InvalidMultiplierReturnsBlock(t *testing.T) {
	// size_multiplier 0.33 is not in {1.0, 0.75, 0.5, 0.25, 0.0}
	raw := `{"schema_version":"2.1","review":{"action":"REDUCE_SIZE","confidence":80,"setup_quality":"average","reason_summary":"reduce"},"recommended_adjustment":{"size_multiplier":0.33,"entry_mode":"limit_retest"},"rejection_reason":null}`
	dec, err := ParseVetoResponse(raw)
	if err == nil {
		t.Fatal("expected error for invalid size_multiplier 0.33")
	}
	if dec.Decision != "BLOCK" {
		t.Errorf("expected BLOCK for invalid multiplier, got %s", dec.Decision)
	}
}

func TestParseVetoResponse_ValidResponseStoresRaw(t *testing.T) {
	raw := `{"schema_version":"2.1","request_id":"r1","candidate_id":"c1","review":{"action":"APPROVE","confidence":85,"setup_quality":"good","reason_summary":"strong setup"},"technical_flags":{"market_regime_supports_trade":true},"recommended_adjustment":{"size_multiplier":1.0,"entry_mode":"market","do_not_chase":false},"rejection_reason":null}`
	dec, err := ParseVetoResponse(raw)
	if err != nil {
		t.Fatalf("ParseVetoResponse: %v", err)
	}
	if dec.RawResponse != raw {
		t.Errorf("expected RawResponse to contain original raw JSON")
	}
	if dec.ValidationStatus != "valid" {
		t.Errorf("expected ValidationStatus 'valid', got %s", dec.ValidationStatus)
	}
}

func TestParseVetoResponse_Confidence59In100ScaleReturnsBlock(t *testing.T) {
	// confidence 59 in 0-100 scale -> 0.59 after conversion -> < 0.75 -> BLOCK
	raw := `{"schema_version":"2.1","review":{"action":"APPROVE","confidence":59,"setup_quality":"good","reason_summary":"ok"},"recommended_adjustment":{"size_multiplier":1.0},"rejection_reason":null}`
	dec, err := ParseVetoResponse(raw)
	if err == nil {
		t.Fatal("expected error for confidence 59 (< 0.75 after conversion)")
	}
	if dec.Decision != "BLOCK" {
		t.Errorf("expected BLOCK for low confidence, got %s", dec.Decision)
	}
}
