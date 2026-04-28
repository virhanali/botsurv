package marketdata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/virhan/botsurv/internal/domain"
)

// CandleInserter is the minimal interface needed for backfill insertion.
type CandleInserter interface {
	Insert(ctx context.Context, c domain.Candle) (int64, error)
}

// backfillCandles fetches historical candles from Bybit REST API, persists them,
// and returns the fetched candles so callers can warm in-memory caches.
// If repo is nil, candles are returned without persistence.
func backfillCandles(ctx context.Context, httpClient *http.Client, restURL, symbol, timeframe string, limit int, repo CandleInserter) ([]domain.Candle, error) {
	if limit <= 0 {
		return nil, nil
	}

	endpoint, err := url.JoinPath(restURL, "/v5/market/kline")
	if err != nil {
		return nil, fmt.Errorf("join backfill url: %w", err)
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse backfill url: %w", err)
	}
	q := u.Query()
	q.Set("category", "linear")
	q.Set("symbol", symbol)
	q.Set("interval", mapTimeframeToBybit(timeframe))
	q.Set("limit", strconv.Itoa(limit))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create backfill request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("backfill request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("backfill status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read backfill response: %w", err)
	}

	candles, err := parseKlineRESTResponse(symbol, timeframe, body)
	if err != nil {
		return nil, fmt.Errorf("parse backfill response: %w", err)
	}

	interval, err := timeframeDuration(timeframe)
	if err != nil {
		return nil, fmt.Errorf("backfill timeframe %s: %w", timeframe, err)
	}
	nowMillis := time.Now().UTC().UnixMilli()
	var closed []domain.Candle
	// Small slack to avoid off-by-one with clock drift.
	slackMillis := int64(500)
	for i := range candles {
		if candles[i].OpenTime+interval.Milliseconds() > nowMillis+slackMillis {
			continue
		}
		candles[i].Confirmed = true
		closed = append(closed, candles[i])
		if repo != nil {
			if _, err := repo.Insert(ctx, candles[i]); err != nil {
				return nil, fmt.Errorf("insert backfill candle: %w", err)
			}
		}
	}
	return closed, nil
}

// parseKlineRESTResponse parses Bybit v5 /v5/market/kline JSON response.
// Bybit returns rows newest-first; this function sorts them ascending by OpenTime.
func parseKlineRESTResponse(symbol, timeframe string, body []byte) ([]domain.Candle, error) {
	var envelope struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			List [][]string `json:"list"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("unmarshal kline response: %w", err)
	}
	if envelope.RetCode != 0 {
		return nil, fmt.Errorf("bybit error %d: %s", envelope.RetCode, envelope.RetMsg)
	}

	var candles []domain.Candle
	for _, row := range envelope.Result.List {
		if len(row) < 6 {
			continue
		}
		c, err := parseKlineRESTRow(symbol, timeframe, row)
		if err != nil {
			continue
		}
		candles = append(candles, c)
	}

	sort.Slice(candles, func(i, j int) bool {
		return candles[i].OpenTime < candles[j].OpenTime
	})

	return candles, nil
}

func parseKlineRESTRow(symbol, timeframe string, row []string) (domain.Candle, error) {
	var c domain.Candle
	var err error
	c.Symbol = symbol
	c.Timeframe = timeframe

	c.OpenTime, err = strconv.ParseInt(row[0], 10, 64)
	if err != nil {
		return domain.Candle{}, fmt.Errorf("parse openTime: %w", err)
	}
	c.Open, err = strconv.ParseFloat(row[1], 64)
	if err != nil {
		return domain.Candle{}, fmt.Errorf("parse open: %w", err)
	}
	c.High, err = strconv.ParseFloat(row[2], 64)
	if err != nil {
		return domain.Candle{}, fmt.Errorf("parse high: %w", err)
	}
	c.Low, err = strconv.ParseFloat(row[3], 64)
	if err != nil {
		return domain.Candle{}, fmt.Errorf("parse low: %w", err)
	}
	c.Close, err = strconv.ParseFloat(row[4], 64)
	if err != nil {
		return domain.Candle{}, fmt.Errorf("parse close: %w", err)
	}
	c.Volume, err = strconv.ParseFloat(row[5], 64)
	if err != nil {
		return domain.Candle{}, fmt.Errorf("parse volume: %w", err)
	}
	if len(row) > 6 {
		c.TurnOver, _ = strconv.ParseFloat(row[6], 64)
	}
	return c, nil
}

func timeframeDuration(timeframe string) (time.Duration, error) {
	switch timeframe {
	case "1", "1m":
		return time.Minute, nil
	case "5", "5m":
		return 5 * time.Minute, nil
	case "15", "15m":
		return 15 * time.Minute, nil
	case "30", "30m":
		return 30 * time.Minute, nil
	case "60", "1H":
		return time.Hour, nil
	case "240", "4H":
		return 4 * time.Hour, nil
	case "D", "1d":
		return 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("unsupported timeframe")
	}
}

// mapTimeframeToBybit converts config timeframe values to Bybit API intervals.
// Bybit uses numeric intervals: 1, 5, 15, 30, 60, 240, D.
func mapTimeframeToBybit(tf string) string {
	switch tf {
	case "1m":
		return "1"
	case "5m":
		return "5"
	case "15m":
		return "15"
	case "30m":
		return "30"
	case "1H":
		return "60"
	case "4H":
		return "240"
	case "1d":
		return "D"
	default:
		return tf
	}
}

// mapBybitToTimeframe is the inverse of mapTimeframeToBybit.
// It normalizes incoming Bybit interval strings back to config timeframe values.
func mapBybitToTimeframe(tf string) string {
	switch tf {
	case "1":
		return "1m"
	case "5":
		return "5m"
	case "15":
		return "15m"
	case "30":
		return "30m"
	case "60":
		return "1H"
	case "240":
		return "4H"
	case "D":
		return "1d"
	default:
		return tf
	}
}
