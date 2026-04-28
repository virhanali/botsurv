package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/logger"
)

// Client is the interface for LLM veto requests.
type Client interface {
	// VetoRequest sends a context to the LLM and returns a decision.
	VetoRequest(ctx context.Context, contextJSON string) (domain.LLMDecision, error)
	// DailyCost returns the accumulated daily cost.
	DailyCost() float64
	// ResetDailyCost resets the daily cost counter.
	ResetDailyCost()
}

// MockClient is a test/mock LLM client that always returns a fixed decision.
type MockClient struct {
	Decision domain.LLMDecision
	Err      error
	cost     float64
}

func NewMockClient(decision domain.LLMDecision, err error) *MockClient {
	return &MockClient{Decision: decision, Err: err}
}

func (m *MockClient) VetoRequest(_ context.Context, _ string) (domain.LLMDecision, error) {
	if m.Err != nil {
		return domain.LLMDecision{
			Decision:         "BLOCK",
			Confidence:       0,
			ValidationStatus: "api_error",
		}, m.Err
	}
	m.cost += 0.001 // mock cost
	return m.Decision, nil
}

func (m *MockClient) DailyCost() float64 { return m.cost }
func (m *MockClient) ResetDailyCost()    { m.cost = 0 }

// OpenRouterClient calls the OpenRouter Chat Completions API.
type OpenRouterClient struct {
	cfg       app.LLMConfig
	budget    app.LLMBudgetConfig
	log       *logger.Logger
	client    *http.Client
	dailyCost float64
}

// NewOpenRouterClient creates a new OpenRouter LLM client.
func NewOpenRouterClient(cfg app.LLMConfig, log *logger.Logger) *OpenRouterClient {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &OpenRouterClient{
		cfg:    cfg,
		budget: cfg.Budget,
		log:    log,
		client: &http.Client{Timeout: timeout},
	}
}

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	Temperature    float64         `json:"temperature"`
	MaxTokens      int             `json:"max_tokens"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage *struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// VetoRequest sends a context to the LLM and returns a parsed decision.
func (c *OpenRouterClient) VetoRequest(ctx context.Context, contextJSON string) (domain.LLMDecision, error) {
	// Budget check
	if c.budget.MaxCostUSDPerDay > 0 && c.dailyCost >= c.budget.MaxCostUSDPerDay {
		c.log.Warn("LLM budget exceeded", map[string]any{
			"daily_cost": c.dailyCost,
			"max":        c.budget.MaxCostUSDPerDay,
		})
		return c.fallbackDecision("BLOCK", "LLM_BUDGET_EXCEEDED"), nil
	}

	systemPrompt := `You are a crypto trading veto agent. Analyze the candidate context and respond with ONLY a JSON object. No markdown, no explanation.

Allowed decisions: ALLOW_MARKET, ALLOW_LIMIT_RETEST, REDUCE_SIZE, BLOCK

Required JSON format:
{
  "decision": "ALLOW_MARKET|ALLOW_LIMIT_RETEST|REDUCE_SIZE|BLOCK",
  "confidence": 0.0-1.0,
  "size_multiplier": 1.0|0.75|0.5|0.25|0.0,
  "regime": "trend_up|trend_down|range|chop|unknown",
  "reason_codes": ["code1", "code2"],
  "risk_flags": ["flag1"],
  "notes": "brief explanation"
}`

	reqBody := chatRequest{
		Model: c.cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: contextJSON},
		},
		Temperature:    0,
		MaxTokens:      c.cfg.MaxTokens,
		ResponseFormat: &responseFormat{Type: "json_object"},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return c.fallbackDecision("BLOCK", "REQUEST_MARSHAL_ERROR"), nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return c.fallbackDecision("BLOCK", "REQUEST_CREATE_ERROR"), nil
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+os.Getenv("OPENROUTER_API_KEY"))

	if siteURL := os.Getenv("OPENROUTER_SITE_URL"); siteURL != "" {
		req.Header.Set("HTTP-Referer", siteURL)
	}
	if appName := os.Getenv("OPENROUTER_APP_NAME"); appName != "" {
		req.Header.Set("X-Title", appName)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		c.log.Error("LLM API error", map[string]any{"error": err.Error()})
		return c.fallbackDecision("BLOCK", "API_ERROR"), nil
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return c.fallbackDecision("BLOCK", "RESPONSE_READ_ERROR"), nil
	}

	if resp.StatusCode != http.StatusOK {
		c.log.Error("LLM API non-200", map[string]any{"status": resp.StatusCode, "body": string(respBody)})
		return c.fallbackDecision("BLOCK", fmt.Sprintf("API_STATUS_%d", resp.StatusCode)), nil
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		c.log.Error("LLM response parse error", map[string]any{"body": string(respBody)})
		return c.fallbackDecision("BLOCK", "INVALID_JSON"), nil
	}

	if chatResp.Error != nil {
		c.log.Error("LLM API error in response", map[string]any{"message": chatResp.Error.Message})
		return c.fallbackDecision("BLOCK", "API_ERROR_IN_RESPONSE"), nil
	}

	if len(chatResp.Choices) == 0 {
		return c.fallbackDecision("BLOCK", "NO_CHOICES"), nil
	}

	// Parse the LLM's JSON response
	decision, err := c.parseDecision(chatResp.Choices[0].Message.Content)
	if err != nil {
		c.log.Error("LLM decision parse error", map[string]any{"content": chatResp.Choices[0].Message.Content, "error": err.Error()})
		return c.fallbackDecision("BLOCK", "INVALID_DECISION_JSON"), nil
	}

	decision.RawResponse = chatResp.Choices[0].Message.Content

	// Validate decision
	if err := c.validateDecision(decision); err != nil {
		c.log.Warn("LLM decision validation failed", map[string]any{"error": err.Error(), "decision": decision.Decision})
		return c.fallbackDecision("BLOCK", "VALIDATION_FAILED"), nil
	}

	// Confidence check
	minConf := 0.6
	if decision.Confidence < minConf {
		c.log.Warn("LLM confidence below threshold", map[string]any{"confidence": decision.Confidence, "min": minConf})
		return c.fallbackDecision("BLOCK", "LOW_CONFIDENCE"), nil
	}

	// Budget tracking (rough estimate)
	if chatResp.Usage != nil {
		// Rough: $0.01 per 1K tokens for cheap models
		c.dailyCost += float64(chatResp.Usage.TotalTokens) * 0.00001
	}

	c.log.Info("LLM decision", map[string]any{
		"decision":   decision.Decision,
		"confidence": decision.Confidence,
	})

	return decision, nil
}

func (c *OpenRouterClient) parseDecision(content string) (domain.LLMDecision, error) {
	var decision domain.LLMDecision
	if err := json.Unmarshal([]byte(content), &decision); err != nil {
		return decision, fmt.Errorf("parse decision JSON: %w", err)
	}
	return decision, nil
}

func (c *OpenRouterClient) validateDecision(d domain.LLMDecision) error {
	validDecisions := map[string]bool{
		"ALLOW_MARKET":       true,
		"ALLOW_LIMIT_RETEST": true,
		"REDUCE_SIZE":        true,
		"BLOCK":              true,
	}
	if !validDecisions[d.Decision] {
		return fmt.Errorf("invalid decision: %s", d.Decision)
	}

	validMultipliers := map[float64]bool{1.0: true, 0.75: true, 0.5: true, 0.25: true, 0.0: true}
	if d.Decision == "REDUCE_SIZE" {
		if !validMultipliers[d.SizeMultiplier] {
			return fmt.Errorf("invalid size_multiplier: %.2f", d.SizeMultiplier)
		}
	}

	return nil
}

func (c *OpenRouterClient) fallbackDecision(decision, reason string) domain.LLMDecision {
	return domain.LLMDecision{
		Decision:         decision,
		Confidence:       0,
		SizeMultiplier:   0,
		ReasonCodes:      []string{reason},
		ValidationStatus: "fallback",
		RawResponse:      "",
	}
}

func (c *OpenRouterClient) DailyCost() float64 { return c.dailyCost }
func (c *OpenRouterClient) ResetDailyCost()    { c.dailyCost = 0 }
