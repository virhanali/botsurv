package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/logger"
)

func newTestLogger() *logger.Logger {
	return logger.New(nil, logger.LevelDebug)
}

func TestMain(m *testing.M) {
	if os.Getenv("OPENROUTER_API_KEY") == "" {
		os.Setenv("OPENROUTER_API_KEY", "test-key")
	}
	if os.Getenv("DEEPSEEK_API_KEY") == "" {
		os.Setenv("DEEPSEEK_API_KEY", "test-key")
	}
	code := m.Run()
	os.Exit(code)
}

func TestMockClient_AllowMarket(t *testing.T) {
	mock := NewMockClient(domain.LLMDecision{
		Decision:       "ALLOW_MARKET",
		Confidence:     0.9,
		SizeMultiplier: 1.0,
	}, nil)

	decision, err := mock.VetoRequest(context.Background(), "{}")
	if err != nil {
		t.Fatalf("VetoRequest: %v", err)
	}
	if decision.Decision != "ALLOW_MARKET" {
		t.Errorf("expected ALLOW_MARKET, got %s", decision.Decision)
	}
}

func TestMockClient_ErrorFallsBackToBlock(t *testing.T) {
	mock := NewMockClient(domain.LLMDecision{}, fmt.Errorf("api error"))

	decision, _ := mock.VetoRequest(context.Background(), "{}")
	if decision.Decision != "BLOCK" {
		t.Errorf("expected BLOCK on error, got %s", decision.Decision)
	}
}

func TestOpenRouterClient_ValidAllowMarket(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := chatResponse{
			Choices: []struct {
				Message chatMessageResponse `json:"message"`
			}{
				{Message: chatMessageResponse{
					Content: `{"decision":"ALLOW_MARKET","confidence":0.85,"size_multiplier":1.0,"regime":"trend_up","reason_codes":["good_setup"],"risk_flags":[],"notes":"looks good"}`,
				}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := app.LLMConfig{
		Enabled:        true,
		Provider:       "openrouter",
		BaseURL:        server.URL,
		Model:          "test-model",
		Temperature:    0,
		TimeoutSeconds: 10,
		MaxTokens:      512,
	}

	client := NewOpenRouterClient(cfg, newTestLogger())
	decision, err := client.VetoRequest(context.Background(), `{"symbol":"BTCUSDT"}`)
	if err != nil {
		t.Fatalf("VetoRequest: %v", err)
	}
	if decision.Decision != "ALLOW_MARKET" {
		t.Errorf("expected ALLOW_MARKET, got %s", decision.Decision)
	}
	if decision.Confidence != 0.85 {
		t.Errorf("expected confidence 0.85, got %.2f", decision.Confidence)
	}
	if decision.ValidationStatus == "fallback" {
		t.Error("should not be fallback")
	}
}

func TestOpenRouterClient_ValidReduceSize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := chatResponse{
			Choices: []struct {
				Message chatMessageResponse `json:"message"`
			}{
				{Message: chatMessageResponse{
					Content: `{"decision":"REDUCE_SIZE","confidence":0.7,"size_multiplier":0.5,"regime":"range","reason_codes":["uncertain"],"risk_flags":[],"notes":"reduce"}`,
				}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := app.LLMConfig{
		Enabled:        true,
		BaseURL:        server.URL,
		Model:          "test",
		TimeoutSeconds: 10,
		MaxTokens:      512,
	}

	client := NewOpenRouterClient(cfg, newTestLogger())
	decision, _ := client.VetoRequest(context.Background(), "{}")
	if decision.Decision != "REDUCE_SIZE" {
		t.Errorf("expected REDUCE_SIZE, got %s", decision.Decision)
	}
	if decision.SizeMultiplier != 0.5 {
		t.Errorf("expected size_multiplier 0.5, got %.2f", decision.SizeMultiplier)
	}
}

func TestOpenRouterClient_MalformedJSONFallsBackToBlock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := chatResponse{
			Choices: []struct {
				Message chatMessageResponse `json:"message"`
			}{
				{Message: chatMessageResponse{
					Content: `this is not json`,
				}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := app.LLMConfig{
		Enabled:        true,
		BaseURL:        server.URL,
		Model:          "test",
		TimeoutSeconds: 10,
		MaxTokens:      512,
	}

	client := NewOpenRouterClient(cfg, newTestLogger())
	decision, _ := client.VetoRequest(context.Background(), "{}")
	if decision.Decision != "BLOCK" {
		t.Errorf("expected BLOCK for malformed JSON, got %s", decision.Decision)
	}
}

func TestOpenRouterClient_InvalidDecisionEnumFallsBackToBlock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := chatResponse{
			Choices: []struct {
				Message chatMessageResponse `json:"message"`
			}{
				{Message: chatMessageResponse{
					Content: `{"decision":"INVALID_DECISION","confidence":0.8,"size_multiplier":1.0}`,
				}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := app.LLMConfig{
		Enabled:        true,
		BaseURL:        server.URL,
		Model:          "test",
		TimeoutSeconds: 10,
		MaxTokens:      512,
	}

	client := NewOpenRouterClient(cfg, newTestLogger())
	decision, _ := client.VetoRequest(context.Background(), "{}")
	if decision.Decision != "BLOCK" {
		t.Errorf("expected BLOCK for invalid enum, got %s", decision.Decision)
	}
}

func TestOpenRouterClient_InvalidMultiplierFallsBackToBlock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := chatResponse{
			Choices: []struct {
				Message chatMessageResponse `json:"message"`
			}{
				{Message: chatMessageResponse{
					Content: `{"decision":"REDUCE_SIZE","confidence":0.8,"size_multiplier":0.33}`,
				}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := app.LLMConfig{
		Enabled:        true,
		BaseURL:        server.URL,
		Model:          "test",
		TimeoutSeconds: 10,
		MaxTokens:      512,
	}

	client := NewOpenRouterClient(cfg, newTestLogger())
	decision, _ := client.VetoRequest(context.Background(), "{}")
	if decision.Decision != "BLOCK" {
		t.Errorf("expected BLOCK for invalid multiplier, got %s", decision.Decision)
	}
}

func TestOpenRouterClient_LowConfidenceFallsBackToBlock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := chatResponse{
			Choices: []struct {
				Message chatMessageResponse `json:"message"`
			}{
				{Message: chatMessageResponse{
					Content: `{"decision":"ALLOW_MARKET","confidence":0.3,"size_multiplier":1.0}`,
				}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := app.LLMConfig{
		Enabled:        true,
		BaseURL:        server.URL,
		Model:          "test",
		TimeoutSeconds: 10,
		MaxTokens:      512,
	}

	client := NewOpenRouterClient(cfg, newTestLogger())
	decision, _ := client.VetoRequest(context.Background(), "{}")
	if decision.Decision != "BLOCK" {
		t.Errorf("expected BLOCK for low confidence, got %s", decision.Decision)
	}
}

func TestOpenRouterClient_API500FallsBackToBlock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer server.Close()

	cfg := app.LLMConfig{
		Enabled:        true,
		BaseURL:        server.URL,
		Model:          "test",
		TimeoutSeconds: 10,
		MaxTokens:      512,
	}

	client := NewOpenRouterClient(cfg, newTestLogger())
	decision, _ := client.VetoRequest(context.Background(), "{}")
	if decision.Decision != "BLOCK" {
		t.Errorf("expected BLOCK for API 500, got %s", decision.Decision)
	}
}

func TestOpenRouterClient_BudgetExceededFallsBackToBlock(t *testing.T) {
	cfg := app.LLMConfig{
		Enabled:        true,
		BaseURL:        "http://unused",
		Model:          "test",
		TimeoutSeconds: 10,
		MaxTokens:      512,
		Budget: app.LLMBudgetConfig{
			MaxCostUSDPerDay: 1.0,
		},
	}

	client := NewOpenRouterClient(cfg, newTestLogger())
	client.dailyCost = 5.0 // exceed budget

	decision, _ := client.VetoRequest(context.Background(), "{}")
	if decision.Decision != "BLOCK" {
		t.Errorf("expected BLOCK for budget exceeded, got %s", decision.Decision)
	}
}

func TestOpenRouterClient_NoChoicesFallsBackToBlock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := chatResponse{Choices: nil}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := app.LLMConfig{
		Enabled:        true,
		BaseURL:        server.URL,
		Model:          "test",
		TimeoutSeconds: 10,
		MaxTokens:      512,
	}

	client := NewOpenRouterClient(cfg, newTestLogger())
	decision, _ := client.VetoRequest(context.Background(), "{}")
	if decision.Decision != "BLOCK" {
		t.Errorf("expected BLOCK for no choices, got %s", decision.Decision)
	}
}

func TestOpenRouterClient_AllowLimitRetest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := chatResponse{
			Choices: []struct {
				Message chatMessageResponse `json:"message"`
			}{
				{Message: chatMessageResponse{
					Content: `{"decision":"ALLOW_LIMIT_RETEST","confidence":0.75,"size_multiplier":1.0,"regime":"range","reason_codes":[],"risk_flags":[],"notes":"wait for retest"}`,
				}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := app.LLMConfig{
		Enabled:        true,
		BaseURL:        server.URL,
		Model:          "test",
		TimeoutSeconds: 10,
		MaxTokens:      512,
	}

	client := NewOpenRouterClient(cfg, newTestLogger())
	decision, _ := client.VetoRequest(context.Background(), "{}")
	if decision.Decision != "ALLOW_LIMIT_RETEST" {
		t.Errorf("expected ALLOW_LIMIT_RETEST, got %s", decision.Decision)
	}
}

func TestOpenRouterClient_DailyCostTracking(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := chatResponse{
			Choices: []struct {
				Message chatMessageResponse `json:"message"`
			}{
				{Message: chatMessageResponse{
					Content: `{"decision":"BLOCK","confidence":0.9,"size_multiplier":0}`,
				}},
			},
			Usage: &struct {
				TotalTokens int `json:"total_tokens"`
			}{TotalTokens: 1000},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := app.LLMConfig{
		Enabled:        true,
		BaseURL:        server.URL,
		Model:          "test",
		TimeoutSeconds: 10,
		MaxTokens:      512,
	}

	client := NewOpenRouterClient(cfg, newTestLogger())
	client.VetoRequest(context.Background(), "{}")
	if client.DailyCost() <= 0 {
		t.Error("expected daily cost > 0 after API call")
	}

	client.ResetDailyCost()
	if client.DailyCost() != 0 {
		t.Error("expected daily cost 0 after reset")
	}
}

func TestDeepSeekClient_RequestShape(t *testing.T) {
	var capturedReqBody []byte
	var capturedHeaders http.Header

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		body, _ := io.ReadAll(r.Body)
		capturedReqBody = body

		resp := chatResponse{
			Choices: []struct {
				Message chatMessageResponse `json:"message"`
			}{
				{Message: chatMessageResponse{
					Content: `{"decision":"ALLOW_MARKET","confidence":0.85,"size_multiplier":1.0,"regime":"trend_up","reason_codes":[],"risk_flags":[],"notes":"ok"}`,
				}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	t.Setenv("DEEPSEEK_API_KEY", "ds-test-key")

	cfg := app.LLMConfig{
		Enabled:        true,
		Provider:       "deepseek",
		BaseURL:        server.URL,
		Model:          "deepseek-v4-pro",
		TimeoutSeconds: 10,
		MaxTokens:      512,
	}

	client := NewDeepSeekClient(cfg, newTestLogger())
	decision, err := client.VetoRequest(context.Background(), `{"symbol":"BTCUSDT"}`)
	if err != nil {
		t.Fatalf("VetoRequest: %v", err)
	}
	if decision.Decision != "ALLOW_MARKET" {
		t.Errorf("expected ALLOW_MARKET, got %s", decision.Decision)
	}

	// Verify Authorization header
	auth := capturedHeaders.Get("Authorization")
	if auth != "Bearer ds-test-key" {
		t.Errorf("expected Authorization Bearer ds-test-key, got %s", auth)
	}

	// Verify request body shape
	var reqMap map[string]any
	if err := json.Unmarshal(capturedReqBody, &reqMap); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}

	if reqMap["temperature"] != nil {
		t.Error("deepseek request should not include temperature")
	}

	if reqMap["reasoning_effort"] != "max" {
		t.Errorf("expected reasoning_effort=max, got %v", reqMap["reasoning_effort"])
	}

	thinking, ok := reqMap["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("expected thinking object, got %T", reqMap["thinking"])
	}
	if thinking["type"] != "enabled" {
		t.Errorf("expected thinking.type=enabled, got %v", thinking["type"])
	}
}

func TestDeepSeekClient_IgnoresReasoningContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := chatResponse{
			Choices: []struct {
				Message chatMessageResponse `json:"message"`
			}{
				{Message: chatMessageResponse{
					Content:          `{"decision":"ALLOW_MARKET","confidence":0.85,"size_multiplier":1.0}`,
					ReasoningContent: "Let me think about this setup...",
				}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := app.LLMConfig{
		Enabled:        true,
		Provider:       "deepseek",
		BaseURL:        server.URL,
		Model:          "deepseek-v4-pro",
		TimeoutSeconds: 10,
		MaxTokens:      512,
	}

	client := NewDeepSeekClient(cfg, newTestLogger())
	decision, err := client.VetoRequest(context.Background(), `{}`)
	if err != nil {
		t.Fatalf("VetoRequest: %v", err)
	}
	if decision.Decision != "ALLOW_MARKET" {
		t.Errorf("expected ALLOW_MARKET, got %s", decision.Decision)
	}
	if decision.RawResponse != `{"decision":"ALLOW_MARKET","confidence":0.85,"size_multiplier":1.0}` {
		t.Errorf("RawResponse should be content, not reasoning_content: %s", decision.RawResponse)
	}
}

func TestOpenRouterClient_OptionalHeaders(t *testing.T) {
	var capturedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		resp := chatResponse{
			Choices: []struct {
				Message chatMessageResponse `json:"message"`
			}{
				{Message: chatMessageResponse{
					Content: `{"decision":"BLOCK","confidence":0.9}`,
				}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	t.Setenv("OPENROUTER_API_KEY", "or-test-key")
	t.Setenv("OPENROUTER_SITE_URL", "https://example.com")
	t.Setenv("OPENROUTER_APP_NAME", "BotSurv")

	cfg := app.LLMConfig{
		Enabled:        true,
		Provider:       "openrouter",
		BaseURL:        server.URL,
		Model:          "test",
		Temperature:    0,
		TimeoutSeconds: 10,
		MaxTokens:      512,
	}

	client := NewOpenRouterClient(cfg, newTestLogger())
	_, _ = client.VetoRequest(context.Background(), `{}`)

	if capturedHeaders.Get("Authorization") != "Bearer or-test-key" {
		t.Errorf("expected Authorization Bearer or-test-key, got %s", capturedHeaders.Get("Authorization"))
	}
	if capturedHeaders.Get("HTTP-Referer") != "https://example.com" {
		t.Errorf("expected HTTP-Referer https://example.com, got %s", capturedHeaders.Get("HTTP-Referer"))
	}
	if capturedHeaders.Get("X-Title") != "BotSurv" {
		t.Errorf("expected X-Title BotSurv, got %s", capturedHeaders.Get("X-Title"))
	}
}

func TestOpenRouterClient_MalformedJSONPreservesRawResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := chatResponse{
			Choices: []struct {
				Message chatMessageResponse `json:"message"`
			}{
				{Message: chatMessageResponse{
					Content: `this is not valid json`,
				}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := app.LLMConfig{
		Enabled:        true,
		BaseURL:        server.URL,
		Model:          "test",
		TimeoutSeconds: 10,
		MaxTokens:      512,
	}

	client := NewOpenRouterClient(cfg, newTestLogger())
	decision, _ := client.VetoRequest(context.Background(), `{}`)
	if decision.Decision != "BLOCK" {
		t.Errorf("expected BLOCK, got %s", decision.Decision)
	}
	if decision.RawResponse != "this is not valid json" {
		t.Errorf("expected raw response preserved, got %q", decision.RawResponse)
	}
}

func TestOpenRouterClient_InvalidEnumPreservesRawResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := chatResponse{
			Choices: []struct {
				Message chatMessageResponse `json:"message"`
			}{
				{Message: chatMessageResponse{
					Content: `{"decision":"BUY_EVERYTHING","confidence":0.8,"size_multiplier":1.0}`,
				}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := app.LLMConfig{
		Enabled:        true,
		BaseURL:        server.URL,
		Model:          "test",
		TimeoutSeconds: 10,
		MaxTokens:      512,
	}

	client := NewOpenRouterClient(cfg, newTestLogger())
	decision, _ := client.VetoRequest(context.Background(), `{}`)
	if decision.Decision != "BLOCK" {
		t.Errorf("expected BLOCK, got %s", decision.Decision)
	}
	if decision.RawResponse != `{"decision":"BUY_EVERYTHING","confidence":0.8,"size_multiplier":1.0}` {
		t.Errorf("expected raw response preserved, got %q", decision.RawResponse)
	}
}

func TestOpenRouterClient_LowConfidencePreservesRawResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := chatResponse{
			Choices: []struct {
				Message chatMessageResponse `json:"message"`
			}{
				{Message: chatMessageResponse{
					Content: `{"decision":"ALLOW_MARKET","confidence":0.3,"size_multiplier":1.0}`,
				}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := app.LLMConfig{
		Enabled:        true,
		BaseURL:        server.URL,
		Model:          "test",
		TimeoutSeconds: 10,
		MaxTokens:      512,
	}

	client := NewOpenRouterClient(cfg, newTestLogger())
	decision, _ := client.VetoRequest(context.Background(), `{}`)
	if decision.Decision != "BLOCK" {
		t.Errorf("expected BLOCK, got %s", decision.Decision)
	}
	if decision.RawResponse != `{"decision":"ALLOW_MARKET","confidence":0.3,"size_multiplier":1.0}` {
		t.Errorf("expected raw response preserved, got %q", decision.RawResponse)
	}
}

func TestLiveDeepSeek_Smoke(t *testing.T) {
	if os.Getenv("BOTSURV_TEST_LIVE_LLM") != "1" {
		t.Skip("set BOTSURV_TEST_LIVE_LLM=1 and DEEPSEEK_API_KEY to run live LLM smoke test")
	}
	if os.Getenv("DEEPSEEK_API_KEY") == "" {
		t.Skip("DEEPSEEK_API_KEY not set")
	}

	cfg := app.LLMConfig{
		Enabled:        true,
		Provider:       "deepseek",
		BaseURL:        "https://api.deepseek.com",
		Model:          "deepseek-v4-pro",
		TimeoutSeconds: 60,
		MaxTokens:      4096,
	}

	client := NewDeepSeekClient(cfg, newTestLogger())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	contextJSON := `{"symbol":"BTCUSDT","side":"LONG","setup_type":"breakout","regime":"trend_up","entry_type":"MARKET","proposed_entry":65000,"stop_loss":64000,"take_profit":67000,"rr":2.0,"setup_score":80}`
	decision, err := client.VetoRequest(ctx, contextJSON)
	if err != nil {
		t.Fatalf("live DeepSeek call failed: %v", err)
	}
	if decision.ValidationStatus == "fallback" {
		t.Fatalf("live DeepSeek call fell back instead of returning a model decision: reasons=%v raw=%s", decision.ReasonCodes, decision.RawResponse)
	}

	validDecisions := map[string]bool{"ALLOW_MARKET": true, "ALLOW_LIMIT_RETEST": true, "REDUCE_SIZE": true, "BLOCK": true}
	if !validDecisions[decision.Decision] {
		t.Errorf("invalid decision: %s", decision.Decision)
	}
	if decision.Confidence < 0 || decision.Confidence > 1 {
		t.Errorf("confidence out of range: %f", decision.Confidence)
	}
	if decision.RawResponse == "" {
		t.Error("raw response should be preserved")
	}
	t.Logf("Live DeepSeek decision: %s confidence=%.2f raw=%s", decision.Decision, decision.Confidence, decision.RawResponse)
}
