package alert

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// TelegramService sends alerts via Telegram Bot API.
type TelegramService struct {
	botToken string
	chatID   string
	client   *http.Client
}

// NewTelegramService creates a new Telegram alert service.
// botToken and chatID may be empty; if so, sends are silently skipped.
func NewTelegramService(botToken, chatID string) *TelegramService {
	if botToken == "" {
		botToken = os.Getenv("TELEGRAM_BOT_TOKEN")
	}
	if chatID == "" {
		chatID = os.Getenv("TELEGRAM_CHAT_ID")
	}
	return &TelegramService{
		botToken: botToken,
		chatID:   chatID,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

// Send sends an alert via Telegram. If bot token or chat ID is empty, silently skips.
func (s *TelegramService) Send(ctx context.Context, event AlertEvent) error {
	if s.botToken == "" || s.chatID == "" {
		return nil
	}

	msg := s.formatMessage(event)

	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", s.botToken)
	data := url.Values{
		"chat_id":    {s.chatID},
		"text":       {msg},
		"parse_mode": {"Markdown"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("telegram request create: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram API returned status %d", resp.StatusCode)
	}

	return nil
}

func (s *TelegramService) formatMessage(event AlertEvent) string {
	severityEmoji := map[string]string{
		"info":    "ℹ️",
		"warning": "⚠️",
		"danger":  "🚨",
	}

	emoji := severityEmoji[event.Severity]
	if emoji == "" {
		emoji = "ℹ️"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s *BotSurv Alert* - %s\n", emoji, event.Type))
	if event.Symbol != "" {
		sb.WriteString(fmt.Sprintf("Symbol: `%s`\n", event.Symbol))
	}
	if event.PnL != 0 {
		sb.WriteString(fmt.Sprintf("PnL: $%.2f\n", event.PnL))
	}
	sb.WriteString(fmt.Sprintf("Message: %s\n", event.Message))
	sb.WriteString(fmt.Sprintf("Time: %s", event.Timestamp.Format(time.RFC3339)))

	return sb.String()
}
