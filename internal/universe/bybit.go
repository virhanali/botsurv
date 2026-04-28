package universe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

type bybitInstrument struct {
	Symbol      string `json:"symbol"`
	Status      string `json:"status"`
	BaseCoin    string `json:"baseCoin"`
	QuoteCoin   string `json:"quoteCoin"`
	MinOrderQty string `json:"minOrderQty"`
	TickSize    string `json:"tickSize"`
	QtyStep     string `json:"qtyStep"`
	MaxLeverage string `json:"maxLeverage"`
}

type bybitTicker struct {
	Symbol      string `json:"symbol"`
	Turnover24h string `json:"turnover24h"`
	LastPrice   string `json:"lastPrice"`
	Bid1Price   string `json:"bid1Price"`
	Ask1Price   string `json:"ask1Price"`
}

type bybitInstrumentsResponse struct {
	RetCode int `json:"retCode"`
	Result  struct {
		List []bybitInstrument `json:"list"`
	} `json:"result"`
}

type bybitTickersResponse struct {
	RetCode int `json:"retCode"`
	Result  struct {
		List []bybitTicker `json:"list"`
	} `json:"result"`
}

type bybitClient struct {
	baseURL string
	client  *http.Client
}

func newBybitClient(baseURL string) *bybitClient {
	return &bybitClient{
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *bybitClient) fetchInstruments(ctx context.Context) ([]bybitInstrument, error) {
	u, err := url.Parse(c.baseURL + "/v5/market/instruments-info")
	if err != nil {
		return nil, fmt.Errorf("parse instruments url: %w", err)
	}
	q := u.Query()
	q.Set("category", "linear")
	q.Set("limit", "1000")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create instruments request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch instruments: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("instruments unexpected status: %d", resp.StatusCode)
	}

	var data bybitInstrumentsResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode instruments: %w", err)
	}
	if data.RetCode != 0 {
		return nil, fmt.Errorf("instruments retCode: %d", data.RetCode)
	}
	return data.Result.List, nil
}

func (c *bybitClient) fetchTickers(ctx context.Context) ([]bybitTicker, error) {
	u, err := url.Parse(c.baseURL + "/v5/market/tickers")
	if err != nil {
		return nil, fmt.Errorf("parse tickers url: %w", err)
	}
	q := u.Query()
	q.Set("category", "linear")
	q.Set("limit", "1000")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create tickers request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch tickers: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tickers unexpected status: %d", resp.StatusCode)
	}

	var data bybitTickersResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode tickers: %w", err)
	}
	if data.RetCode != 0 {
		return nil, fmt.Errorf("tickers retCode: %d", data.RetCode)
	}
	return data.Result.List, nil
}
