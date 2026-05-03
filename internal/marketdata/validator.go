package marketdata

import (
	"fmt"
	"math"
	"time"

	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/domain"
)

// CandleValidationInput describes a candle batch validation request.
type CandleValidationInput struct {
	Symbol              string
	Timeframe           string
	Candles             []domain.Candle
	MinRequired         int
	MaxDataAgeSeconds   int
	RequireClosedLatest bool
	Now                 time.Time
}

// CandleValidationResult contains validated/partitioned candles.
type CandleValidationResult struct {
	Symbol           string
	Timeframe        string
	Candles          []domain.Candle
	ClosedCandles    []domain.Candle
	InProgressCandle *domain.Candle
	LatestCandle     domain.Candle
	LatestIsClosed   bool
}

// Validator validates candle batches before downstream engines use them.
type Validator struct {
	cfg app.DataValidationConfig
}

// NewValidator creates a market data validator.
func NewValidator(cfg app.DataValidationConfig) *Validator {
	return &Validator{cfg: cfg}
}

// ValidateCandleBatch validates structural/data integrity for a candle batch.
// If RequireClosedLatest is true and the latest candle is in-progress, it strips
// the in-progress candle and validates against the remaining closed candles.
func (v *Validator) ValidateCandleBatch(in CandleValidationInput) (CandleValidationResult, error) {
	if len(in.Candles) == 0 {
		return CandleValidationResult{}, fmt.Errorf("validation failed: empty candle batch")
	}

	// Handle in-progress latest candle before minRequired check
	interval, err := timeframeDuration(in.Timeframe)
	if err != nil {
		return CandleValidationResult{}, fmt.Errorf("validation failed: unsupported timeframe %q", in.Timeframe)
	}
	intervalMS := interval.Milliseconds()

	// Strip in-progress latest candle if not closed.
	// MinRequired applies to closed candles used downstream; do not reduce it.
	if in.RequireClosedLatest && !in.Candles[len(in.Candles)-1].Confirmed {
		if len(in.Candles) < 2 {
			return CandleValidationResult{}, fmt.Errorf("validation failed: latest candle is in-progress but no closed candles available")
		}
		in.Candles = in.Candles[:len(in.Candles)-1]
	}

	minRequired := in.MinRequired
	if minRequired <= 0 {
		minRequired = v.cfg.MinCandlesOrDefault()
	}
	if len(in.Candles) < minRequired {
		return CandleValidationResult{}, fmt.Errorf("validation failed: insufficient candles: have %d need %d", len(in.Candles), minRequired)
	}

	var closed []domain.Candle
	var inProgress *domain.Candle
	for i := range in.Candles {
		c := in.Candles[i]
		if invalidOHLCV(c) {
			return CandleValidationResult{}, fmt.Errorf("validation failed: invalid OHLCV at index=%d open_time=%d", i, c.OpenTime)
		}
		if i > 0 {
			prev := in.Candles[i-1]
			if c.OpenTime <= prev.OpenTime {
				return CandleValidationResult{}, fmt.Errorf("validation failed: non-monotonic timestamp at index=%d prev=%d current=%d", i, prev.OpenTime, c.OpenTime)
			}
		}
		if c.Confirmed {
			closed = append(closed, c)
			continue
		}
		if i != len(in.Candles)-1 {
			return CandleValidationResult{}, fmt.Errorf("validation failed: in-progress candle appears before latest at index=%d", i)
		}
		cp := c
		inProgress = &cp
	}

	latest := in.Candles[len(in.Candles)-1]

	reference := in.Now.UTC()
	if reference.IsZero() {
		reference = time.Now().UTC()
	}
	maxAgeSec := in.MaxDataAgeSeconds
	if maxAgeSec <= 0 {
		maxAgeSec = v.cfg.MaxDataAgeSecondsFor(in.Timeframe)
	}
	latestClose := time.UnixMilli(latest.OpenTime + intervalMS).UTC()
	age := reference.Sub(latestClose)
	if age > time.Duration(maxAgeSec)*time.Second {
		return CandleValidationResult{}, fmt.Errorf("validation failed: stale data age=%s max=%ds", age.Truncate(time.Second), maxAgeSec)
	}

	return CandleValidationResult{
		Symbol:           in.Symbol,
		Timeframe:        in.Timeframe,
		Candles:          in.Candles,
		ClosedCandles:    closed,
		InProgressCandle: inProgress,
		LatestCandle:     latest,
		LatestIsClosed:   latest.Confirmed,
	}, nil
}

func invalidOHLCV(c domain.Candle) bool {
	return invalidPositiveValue(c.Open) ||
		invalidPositiveValue(c.High) ||
		invalidPositiveValue(c.Low) ||
		invalidPositiveValue(c.Close) ||
		invalidNonNegativeValue(c.Volume)
}

func invalidPositiveValue(v float64) bool {
	return math.IsNaN(v) || math.IsInf(v, 0) || v <= 0
}

func invalidNonNegativeValue(v float64) bool {
	return math.IsNaN(v) || math.IsInf(v, 0) || v < 0
}
