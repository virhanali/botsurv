package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/logger"
)

// ReviewClient sends review requests to an LLM and returns structured responses.
type ReviewClient interface {
	Review(ctx context.Context, req ReviewRequest) (*ReviewResult, error)
	DailyCost() float64
	ResetDailyCost()
}

// reviewHTTPClient is the HTTP-based LLM review client.
type reviewHTTPClient struct {
	cfg       app.LLMReviewConfig
	log       *logger.Logger
	http      *http.Client
	dailyCost atomic.Int64 // scaled by 10_000_000_000 to avoid float races (H4)
	provider  string
}

// NewReviewClient creates a new LLM review client.
func NewReviewClient(cfg app.LLMReviewConfig, log *logger.Logger) ReviewClient {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	return &reviewHTTPClient{
		cfg:      cfg.WithDefaults(),
		log:      log,
		http:     &http.Client{Timeout: timeout},
		provider: cfg.Provider,
	}
}

func (c *reviewHTTPClient) Review(ctx context.Context, req ReviewRequest) (*ReviewResult, error) {
	start := time.Now()
	result := &ReviewResult{Called: true}

	// Budget check
	if c.cfg.DailyCostCapUSD > 0 && float64(c.dailyCost.Load())/10_000_000_000 >= c.cfg.DailyCostCapUSD {
		c.log.Warn("LLM review daily cost cap reached", map[string]any{
			"daily_cost": float64(c.dailyCost.Load()) / 10_000_000_000,
			"cap":        c.cfg.DailyCostCapUSD,
		})
		result.Called = false
		result.Valid = false
		result.InvalidReason = "DAILY_COST_CAP_REACHED"
		return result, nil
	}

	// Build prompt
	allowedActions := ""
	if len(req.Constraints.AllowedActions) > 0 {
		allowedActions = fmt.Sprintf("%v", req.Constraints.AllowedActions)
	}
	systemPrompt := ReviewSystemPromptV1(allowedActions)

	// Serialize request
	reqJSON, err := json.Marshal(req)
	if err != nil {
		result.Valid = false
		result.InvalidReason = "REQUEST_MARSHAL_ERROR"
		return result, nil
	}

	// Build HTTP request
	apiKey := c.getAPIKey()
	if apiKey == "" {
		c.log.Error("LLM review API key not set", nil)
		result.Valid = false
		result.InvalidReason = "MISSING_API_KEY"
		return result, nil
	}

	var httpBody []byte
	switch c.provider {
	case "deepseek":
		chatReq := deepSeekChatRequest{
			Model: c.cfg.Model,
			Messages: []chatMessage{
				{Role: "system", Content: systemPrompt},
				{Role: "user", Content: string(reqJSON)},
			},
			MaxTokens: c.cfg.MaxOutputTokens,
		}
		httpBody, err = json.Marshal(chatReq)
	default: // openrouter
		chatReq := openRouterChatRequest{
			Model:          c.cfg.Model,
			Messages:       []chatMessage{{Role: "system", Content: systemPrompt}, {Role: "user", Content: string(reqJSON)}},
			Temperature:    0,
			MaxTokens:      c.cfg.MaxOutputTokens,
			ResponseFormat: &responseFormat{Type: "json_object"},
		}
		httpBody, err = json.Marshal(chatReq)
	}
	if err != nil {
		result.Valid = false
		result.InvalidReason = "REQUEST_MARSHAL_ERROR"
		return result, nil
	}

	baseURL := c.cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(httpBody))
	if err != nil {
		result.Valid = false
		result.InvalidReason = "REQUEST_CREATE_ERROR"
		return result, nil
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	var resp *http.Response
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt) * time.Second
			if backoff > 5*time.Second {
				backoff = 5 * time.Second
			}
			select {
			case <-ctx.Done():
				result.Valid = false
				result.InvalidReason = "TIMEOUT"
				result.LatencyMs = time.Since(start).Milliseconds()
				return result, nil
			case <-time.After(backoff):
			}
		}
		retryReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(httpBody))
		if err != nil {
			lastErr = err
			continue
		}
		retryReq.Header = httpReq.Header.Clone()
		resp, lastErr = c.http.Do(retryReq)
		if lastErr != nil {
			continue
		}
		if resp.StatusCode < 500 {
			break
		}
		resp.Body.Close()
	}

	result.LatencyMs = time.Since(start).Milliseconds()

	if lastErr != nil {
		c.log.Error("LLM review API error after retries", map[string]any{"error": lastErr.Error()})
		result.Valid = false
		result.InvalidReason = "API_ERROR"
		return result, nil
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		result.Valid = false
		result.InvalidReason = "RESPONSE_READ_ERROR"
		return result, nil
	}

	if resp.StatusCode != http.StatusOK {
		c.log.Error("LLM review API non-200", map[string]any{"status": resp.StatusCode, "body": string(respBody)})
		result.Valid = false
		result.InvalidReason = fmt.Sprintf("API_STATUS_%d", resp.StatusCode)
		result.RawResponse = string(respBody)
		return result, nil
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		c.log.Error("LLM review response parse error", map[string]any{"body": string(respBody)})
		result.Valid = false
		result.InvalidReason = "INVALID_JSON"
		result.RawResponse = string(respBody)
		return result, nil
	}

	if chatResp.Error != nil {
		c.log.Error("LLM review API error in response", map[string]any{"message": chatResp.Error.Message})
		result.Valid = false
		result.InvalidReason = "API_ERROR_IN_RESPONSE"
		result.RawResponse = string(respBody)
		return result, nil
	}

	if len(chatResp.Choices) == 0 {
		result.Valid = false
		result.InvalidReason = "NO_CHOICES"
		result.RawResponse = string(respBody)
		return result, nil
	}

	rawContent := chatResp.Choices[0].Message.Content
	result.RawResponse = rawContent

	// Track tokens
	if chatResp.Usage != nil {
		result.TokensIn = chatResp.Usage.TotalTokens / 2
		result.TokensOut = chatResp.Usage.TotalTokens / 2
		result.CostUSD = float64(chatResp.Usage.TotalTokens) * 0.00001
		c.dailyCost.Add(int64(result.CostUSD * 10_000_000_000))
	}

	// Parse the response
	var reviewResp ReviewResponse
	if err := json.Unmarshal([]byte(rawContent), &reviewResp); err != nil {
		result.Valid = false
		result.InvalidReason = "RESPONSE_JSON_PARSE_FAILED"
		return result, nil
	}
	result.Response = &reviewResp
	result.Valid = true

	return result, nil
}

func (c *reviewHTTPClient) getAPIKey() string {
	switch c.provider {
	case "deepseek":
		return os.Getenv("DEEPSEEK_API_KEY")
	default:
		if key := os.Getenv("OPENROUTER_API_KEY"); key != "" {
			return key
		}
		return os.Getenv("DEEPSEEK_API_KEY")
	}
}

func (c *reviewHTTPClient) DailyCost() float64 { return float64(c.dailyCost.Load()) / 10_000_000_000 }
func (c *reviewHTTPClient) ResetDailyCost()    { c.dailyCost.Store(0) }
