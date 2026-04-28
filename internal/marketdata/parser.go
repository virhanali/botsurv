package marketdata

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/virhan/botsurv/internal/domain"
)

// parseKlineMessage parses a Bybit v5 public WebSocket kline message.
// It returns the first candle for convenience; callers that need all candles
// in a batched frame should use parseKlineMessages.
func parseKlineMessage(payload []byte) (symbol, timeframe string, candle domain.Candle, err error) {
	symbol, timeframe, candles, err := parseKlineMessages(payload)
	if err != nil {
		return "", "", domain.Candle{}, err
	}
	if len(candles) == 0 {
		return "", "", domain.Candle{}, fmt.Errorf("empty kline data")
	}
	return symbol, timeframe, candles[0], nil
}

// parseKlineMessages parses a Bybit v5 public WebSocket kline message and
// returns all candles in the data array. Bybit can batch multiple candles
// in a single frame.
func parseKlineMessages(payload []byte) (symbol, timeframe string, candles []domain.Candle, err error) {
	var msg struct {
		Topic string          `json:"topic"`
		Data  json.RawMessage `json:"data"`
	}
	if err = json.Unmarshal(payload, &msg); err != nil {
		return "", "", nil, fmt.Errorf("unmarshal kline message: %w", err)
	}

	topicParts := splitTopic(msg.Topic)
	if len(topicParts) != 3 || topicParts[0] != "kline" {
		return "", "", nil, fmt.Errorf("invalid kline topic: %s", msg.Topic)
	}
	timeframe = mapBybitToTimeframe(topicParts[1])
	symbol = topicParts[2]

	items, err := unmarshalMapSlice(msg.Data)
	if err != nil {
		single, err2 := unmarshalMap(msg.Data)
		if err2 != nil {
			return "", "", nil, fmt.Errorf("unmarshal kline data: %w", err)
		}
		items = []map[string]interface{}{single}
	}
	if len(items) == 0 {
		return "", "", nil, fmt.Errorf("empty kline data")
	}

	for _, item := range items {
		c, err := mapToCandle(symbol, timeframe, item)
		if err != nil {
			return "", "", nil, fmt.Errorf("map kline data: %w", err)
		}
		candles = append(candles, c)
	}
	return symbol, timeframe, candles, nil
}

// parseTickerMessage parses a Bybit v5 public WebSocket ticker message.
// It returns hasPrice=false when the message is a delta that omits lastPrice,
// which callers should treat as a no-op.
func parseTickerMessage(payload []byte) (symbol string, price float64, ts time.Time, hasPrice bool, err error) {
	var msg struct {
		Topic string          `json:"topic"`
		Type  string          `json:"type"`
		Ts    int64           `json:"ts"`
		Data  json.RawMessage `json:"data"`
	}
	if err = json.Unmarshal(payload, &msg); err != nil {
		return "", 0, time.Time{}, false, fmt.Errorf("unmarshal ticker message: %w", err)
	}

	topicParts := splitTopic(msg.Topic)
	if len(topicParts) != 2 || topicParts[0] != "tickers" {
		return "", 0, time.Time{}, false, fmt.Errorf("invalid ticker topic: %s", msg.Topic)
	}
	symbol = topicParts[1]

	obj, err := unmarshalMap(msg.Data)
	if err != nil {
		arr, err2 := unmarshalMapSlice(msg.Data)
		if err2 != nil {
			return "", 0, time.Time{}, false, fmt.Errorf("unmarshal ticker data: %w", err)
		}
		if len(arr) == 0 {
			return "", 0, time.Time{}, false, fmt.Errorf("empty ticker data")
		}
		obj = arr[0]
	}

	priceStr, ok := getString(obj, "lastPrice")
	if !ok {
		if msg.Type == "delta" {
			if msg.Ts > 0 {
				ts = time.UnixMilli(msg.Ts)
			} else {
				ts = time.Now().UTC()
			}
			return symbol, 0, ts, false, nil
		}
		return "", 0, time.Time{}, false, fmt.Errorf("missing lastPrice in ticker")
	}
	price, err = strconv.ParseFloat(priceStr, 64)
	if err != nil {
		return "", 0, time.Time{}, false, fmt.Errorf("parse lastPrice: %w", err)
	}

	if msg.Ts > 0 {
		ts = time.UnixMilli(msg.Ts)
	} else {
		ts = time.Now().UTC()
	}
	return symbol, price, ts, true, nil
}

func mapToCandle(symbol, timeframe string, m map[string]interface{}) (domain.Candle, error) {
	c := domain.Candle{
		Symbol:    symbol,
		Timeframe: timeframe,
	}
	var err error

	c.OpenTime, err = getInt64(m, "start")
	if err != nil {
		return domain.Candle{}, fmt.Errorf("start: %w", err)
	}
	c.Open, err = getFloat(m, "open")
	if err != nil {
		return domain.Candle{}, fmt.Errorf("open: %w", err)
	}
	c.High, err = getFloat(m, "high")
	if err != nil {
		return domain.Candle{}, fmt.Errorf("high: %w", err)
	}
	c.Low, err = getFloat(m, "low")
	if err != nil {
		return domain.Candle{}, fmt.Errorf("low: %w", err)
	}
	c.Close, err = getFloat(m, "close")
	if err != nil {
		return domain.Candle{}, fmt.Errorf("close: %w", err)
	}
	c.Volume, err = getFloat(m, "volume")
	if err != nil {
		return domain.Candle{}, fmt.Errorf("volume: %w", err)
	}
	if v, err := getFloat(m, "turnover"); err == nil {
		c.TurnOver = v
	} else if v, err := getFloat(m, "turnOver"); err == nil {
		c.TurnOver = v
	}

	if confirm, ok := m["confirm"]; ok {
		switch v := confirm.(type) {
		case bool:
			c.Confirmed = v
		case string:
			c.Confirmed = v == "true" || v == "1"
		case float64:
			c.Confirmed = v != 0
		case json.Number:
			c.Confirmed = v.String() != "0"
		}
	}

	return c, nil
}

func splitTopic(topic string) []string {
	parts := make([]string, 0, 4)
	start := 0
	for i := 0; i < len(topic); i++ {
		if topic[i] == '.' {
			parts = append(parts, topic[start:i])
			start = i + 1
		}
	}
	parts = append(parts, topic[start:])
	return parts
}

func unmarshalMap(data []byte) (map[string]interface{}, error) {
	var m map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

func unmarshalMapSlice(data []byte) ([]map[string]interface{}, error) {
	var arr []map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&arr); err != nil {
		return nil, err
	}
	return arr, nil
}

func getString(m map[string]interface{}, key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", false
	}
	switch s := v.(type) {
	case string:
		return s, true
	case []byte:
		return string(s), true
	case json.Number:
		return s.String(), true
	case float64:
		return strconv.FormatFloat(s, 'f', -1, 64), true
	default:
		return fmt.Sprint(v), true
	}
}

func getFloat(m map[string]interface{}, key string) (float64, error) {
	s, ok := getString(m, key)
	if !ok {
		return 0, fmt.Errorf("missing %s", key)
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", key, err)
	}
	return f, nil
}

func getInt64(m map[string]interface{}, key string) (int64, error) {
	s, ok := getString(m, key)
	if !ok {
		return 0, fmt.Errorf("missing %s", key)
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i, nil
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int64(f), nil
	}
	return 0, fmt.Errorf("invalid %s", key)
}

// parseOrderBookMessage parses a Bybit v5 public WebSocket orderbook message.
// It returns the symbol, whether the message is a snapshot, the sequence numbers,
// and the bid/ask levels.
// For snapshots: prevSeq=0, seq=data.seq
// For deltas: prevSeq=data.u, seq=data.seq
func parseOrderBookMessage(payload []byte) (symbol string, isSnapshot bool, seq, prevSeq int64, bids, asks []domain.OrderBookLevel, err error) {
	var msg struct {
		Topic string          `json:"topic"`
		Type  string          `json:"type"`
		Data  json.RawMessage `json:"data"`
	}
	if err = json.Unmarshal(payload, &msg); err != nil {
		return "", false, 0, 0, nil, nil, fmt.Errorf("unmarshal orderbook message: %w", err)
	}

	topicParts := splitTopic(msg.Topic)
	if len(topicParts) != 3 || topicParts[0] != "orderbook" {
		return "", false, 0, 0, nil, nil, fmt.Errorf("invalid orderbook topic: %s", msg.Topic)
	}
	symbol = topicParts[2]
	isSnapshot = msg.Type == "snapshot"

	var data struct {
		Symbol string     `json:"s"`
		Bids   [][]string `json:"b"`
		Asks   [][]string `json:"a"`
		U      int64      `json:"u"`
		Seq    int64      `json:"seq"`
	}
	if err = json.Unmarshal(msg.Data, &data); err != nil {
		// Try alternate format where data is the object directly (not wrapped)
		if err2 := json.Unmarshal(payload, &data); err2 != nil {
			return symbol, false, 0, 0, nil, nil, fmt.Errorf("unmarshal orderbook data: %w", err)
		}
		if data.Symbol != "" {
			symbol = data.Symbol
		}
	} else {
		if data.Symbol != "" {
			symbol = data.Symbol
		}
	}

	seq = data.Seq
	if isSnapshot {
		prevSeq = 0
	} else {
		prevSeq = data.U
	}

	bids, err = parseLevels(data.Bids)
	if err != nil {
		return symbol, isSnapshot, seq, prevSeq, nil, nil, fmt.Errorf("parse bids: %w", err)
	}
	asks, err = parseLevels(data.Asks)
	if err != nil {
		return symbol, isSnapshot, seq, prevSeq, nil, nil, fmt.Errorf("parse asks: %w", err)
	}
	return symbol, isSnapshot, seq, prevSeq, bids, asks, nil
}

func parseLevels(rows [][]string) ([]domain.OrderBookLevel, error) {
	levels := make([]domain.OrderBookLevel, 0, len(rows))
	for _, row := range rows {
		if len(row) < 2 {
			continue
		}
		price, err := strconv.ParseFloat(row[0], 64)
		if err != nil {
			return nil, fmt.Errorf("parse level price: %w", err)
		}
		size, err := strconv.ParseFloat(row[1], 64)
		if err != nil {
			return nil, fmt.Errorf("parse level size: %w", err)
		}
		levels = append(levels, domain.OrderBookLevel{Price: price, Size: size})
	}
	return levels, nil
}

// parsePublicTradeMessage parses a Bybit v5 public WebSocket publicTrade message.
func parsePublicTradeMessage(payload []byte) (symbol string, trades []domain.PublicTrade, err error) {
	var msg struct {
		Topic string          `json:"topic"`
		Type  string          `json:"type"`
		Ts    int64           `json:"ts"`
		Data  json.RawMessage `json:"data"`
	}
	if err = json.Unmarshal(payload, &msg); err != nil {
		return "", nil, fmt.Errorf("unmarshal publicTrade message: %w", err)
	}

	topicParts := splitTopic(msg.Topic)
	if len(topicParts) != 2 || topicParts[0] != "publicTrade" {
		return "", nil, fmt.Errorf("invalid publicTrade topic: %s", msg.Topic)
	}
	symbol = topicParts[1]

	var items []struct {
		Timestamp int64  `json:"T"`
		Symbol    string `json:"s"`
		Price     string `json:"p"`
		Size      string `json:"v"`
		Side      string `json:"S"`
	}
	if err = json.Unmarshal(msg.Data, &items); err != nil {
		// Try single object
		var single struct {
			Timestamp int64  `json:"T"`
			Symbol    string `json:"s"`
			Price     string `json:"p"`
			Size      string `json:"v"`
			Side      string `json:"S"`
		}
		if err2 := json.Unmarshal(msg.Data, &single); err2 != nil {
			return symbol, nil, fmt.Errorf("unmarshal publicTrade data: %w", err)
		}
		items = []struct {
			Timestamp int64  `json:"T"`
			Symbol    string `json:"s"`
			Price     string `json:"p"`
			Size      string `json:"v"`
			Side      string `json:"S"`
		}{single}
	}

	trades = make([]domain.PublicTrade, 0, len(items))
	for _, item := range items {
		price, err := strconv.ParseFloat(item.Price, 64)
		if err != nil {
			continue
		}
		size, err := strconv.ParseFloat(item.Size, 64)
		if err != nil {
			continue
		}
		ts := time.Now().UTC()
		if item.Timestamp > 0 {
			ts = time.UnixMilli(item.Timestamp).UTC()
		} else if msg.Ts > 0 {
			ts = time.UnixMilli(msg.Ts).UTC()
		}
		if item.Symbol != "" {
			symbol = item.Symbol
		}
		trades = append(trades, domain.PublicTrade{
			Symbol:    symbol,
			Price:     price,
			Size:      size,
			Side:      item.Side,
			Timestamp: ts,
		})
	}
	return symbol, trades, nil
}
