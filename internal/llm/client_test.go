package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/logger"
)

func newTestLogger() *logger.Logger {
	return logger.New(nil, logger.LevelDebug)
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
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{
				{Message: struct {
					Content string `json:"content"`
				}{
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
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{
				{Message: struct {
					Content string `json:"content"`
				}{
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
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{
				{Message: struct {
					Content string `json:"content"`
				}{
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
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{
				{Message: struct {
					Content string `json:"content"`
				}{
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
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{
				{Message: struct {
					Content string `json:"content"`
				}{
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
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{
				{Message: struct {
					Content string `json:"content"`
				}{
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
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{
				{Message: struct {
					Content string `json:"content"`
				}{
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
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{
				{Message: struct {
					Content string `json:"content"`
				}{
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
