package app

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

// UserConfig is the top-level application configuration.
type UserConfig struct {
	App             AppConfig             `yaml:"app"`
	Database        DatabaseConfig        `yaml:"database"`
	MarketData      MarketDataConfig      `yaml:"market_data"`
	DataValidation  DataValidationConfig  `yaml:"data_validation"`
	HardBlocks      HardBlocksConfig      `yaml:"hard_blocks"`
	IndicatorEngine IndicatorEngineConfig `yaml:"indicator_engine"`
	MarketRegime    MarketRegimeConfig    `yaml:"market_regime"`
	Scoring         ScoringConfig         `yaml:"scoring"`
	Broker          BrokerConfig          `yaml:"broker"`
	Universe        UniverseConfig        `yaml:"universe"`
	Strategy        StrategyConfig        `yaml:"strategy"`
	LLMRouting      LLMRoutingConfig      `yaml:"llm_routing"`
	LLM             LLMConfig             `yaml:"llm"`
	LLMReview       LLMReviewConfig       `yaml:"llm_review"`
	PortfolioRisk   PortfolioRiskConfig   `yaml:"portfolio_risk"`
	Sizing          SizingConfig          `yaml:"sizing"`
	Risk            RiskConfig            `yaml:"risk"`
	Alerts          AlertsConfig          `yaml:"alerts"`
	Backtest        BacktestConfig        `yaml:"backtest"`
}

// AppConfig contains application-level settings.
type AppConfig struct {
	Mode                 string `yaml:"mode"`
	LogLevel             string `yaml:"log_level"`
	JSONLogs             bool   `yaml:"json_logs"`
	CycleIntervalSeconds int    `yaml:"cycle_interval_seconds"`
	MaxCycleOverlap      bool   `yaml:"max_cycle_overlap"`
	Timezone             string `yaml:"timezone"`
	LiveConfirmed        bool   `yaml:"live_confirmed"`
}

// PoolConfig contains database connection pool settings.
type PoolConfig struct {
	MaxOpenConns    int `yaml:"max_open_conns"`
	MaxIdleConns    int `yaml:"max_idle_conns"`
	ConnMaxLifetime int `yaml:"conn_max_lifetime_seconds"`
}

// DatabaseConfig contains database connection settings.
type DatabaseConfig struct {
	Driver string     `yaml:"driver"`
	DSN    string     `yaml:"dsn"`
	Pool   PoolConfig `yaml:"pool"`
}

// MarketDataConfig contains market data provider settings.
type MarketDataConfig struct {
	Provider                  string        `yaml:"provider"`
	Symbols                   SymbolsConfig `yaml:"symbols"`
	WSURL                     string        `yaml:"ws_url"`
	RESTURL                   string        `yaml:"rest_url"`
	ReconnectIntervalSeconds  int           `yaml:"reconnect_interval_seconds"`
	StaleDataThresholdSeconds int           `yaml:"stale_data_threshold_seconds"`
	BackfillCandles           int           `yaml:"backfill_candles"`
	StartTimeoutSeconds       int           `yaml:"start_timeout_seconds"`
	Timeframes                []string      `yaml:"timeframes"`
	OrderbookDepth            int           `yaml:"orderbook_depth"`
	TradeFlowWindows          []int         `yaml:"trade_flow_windows"`
}

// DataValidationConfig contains candle/snapshot validation settings.
type DataValidationConfig struct {
	MinCandles        int            `yaml:"min_candles"`
	MaxDataAgeSeconds map[string]int `yaml:"max_data_age_seconds"`
}

// HardBlocksConfig contains hard block threshold settings.
type HardBlocksConfig struct {
	MaxSpreadPct         float64 `yaml:"max_spread_pct"`
	MaxFundingAbsPct     float64 `yaml:"max_funding_abs_pct"`
	BTCFlashCrash5mPct   float64 `yaml:"btc_flash_crash_5m_pct"`
	DailyMaxLossPct      float64 `yaml:"daily_max_loss_pct"`
	MaxConsecutiveLosses int     `yaml:"max_consecutive_losses"`
	CooldownAfterLossMin int     `yaml:"cooldown_after_loss_min"`
	CooldownAfterWinMin  int     `yaml:"cooldown_after_win_min"`
}

// IndicatorEngineConfig controls indicator snapshot parameters.
type IndicatorEngineConfig struct {
	EMA20Period             int     `yaml:"ema20_period"`
	EMA50Period             int     `yaml:"ema50_period"`
	EMA200Period            int     `yaml:"ema200_period"`
	RSIPeriod               int     `yaml:"rsi_period"`
	MACDFastPeriod          int     `yaml:"macd_fast_period"`
	MACDSlowPeriod          int     `yaml:"macd_slow_period"`
	MACDSignalPeriod        int     `yaml:"macd_signal_period"`
	ATRPeriod               int     `yaml:"atr_period"`
	VolumeMAPeriod          int     `yaml:"volume_ma_period"`
	SwingLookback           int     `yaml:"swing_lookback"`
	RecentSwingCount        int     `yaml:"recent_swing_count"`
	SupportResistanceATRTol float64 `yaml:"support_resistance_atr_tolerance"`
}

// MarketRegimeConfig controls BTC/BTCD regime filters and relative strength.
type MarketRegimeConfig struct {
	BTCDumpShortPct       float64                `yaml:"btc_dump_short_pct"`
	BTCDumpMediumPct      float64                `yaml:"btc_dump_medium_pct"`
	BTCNearLevelATRBuffer float64                `yaml:"btc_near_level_atr_buffer"`
	BTCDRisingFastPct     float64                `yaml:"btcd_rising_fast_pct"`
	RelativeStrength      RelativeStrengthConfig `yaml:"relative_strength"`
}

// RelativeStrengthConfig controls classification thresholds.
type RelativeStrengthConfig struct {
	StrongOutperformPct   float64 `yaml:"strong_outperform_pct"`
	StrongUnderperformPct float64 `yaml:"strong_underperform_pct"`
	NeutralBandPct        float64 `yaml:"neutral_band_pct"`
	SmoothedEMAPeriod     int     `yaml:"smoothed_ema_period"`
}

// ScoringConfig controls score thresholds and action mapping.
type ScoringConfig struct {
	Mode           string                 `yaml:"mode"`
	ScoringVersion string                 `yaml:"scoring_version"`
	Balanced       ScoringThresholdConfig `yaml:"balanced"`
	Aggressive     ScoringThresholdConfig `yaml:"aggressive"`
}

// ScoringThresholdConfig contains score cutoff thresholds.
type ScoringThresholdConfig struct {
	AllowMarket     float64 `yaml:"allow_market"`
	AllowRetestOnly float64 `yaml:"allow_retest_only"`
	ReduceSize      float64 `yaml:"reduce_size"`
}

// SymbolsConfig controls which symbols to track.
type SymbolsConfig struct {
	Mode         string   `yaml:"mode"`
	ExplicitList []string `yaml:"explicit_list"`
}

// BrokerConfig contains broker settings.
type BrokerConfig struct {
	Provider string      `yaml:"provider"`
	Paper    PaperConfig `yaml:"paper"`
}

// PaperConfig contains paper broker simulation settings.
type PaperConfig struct {
	StartingBalanceUSD float64 `yaml:"starting_balance_usd"`
	FeeMakerBps        float64 `yaml:"fee_maker_bps"`
	FeeTakerBps        float64 `yaml:"fee_taker_bps"`
	SlippageModel      string  `yaml:"slippage_model"`
	SlippageBps        float64 `yaml:"slippage_bps"`
	DefaultLeverage    float64 `yaml:"default_leverage"`
}

// UniverseConfig contains universe scanner settings.
type UniverseConfig struct {
	Mode                    string                        `yaml:"mode"`
	RefreshIntervalMinutes  int                           `yaml:"refresh_interval_minutes"`
	LightScanMaxSymbols     int                           `yaml:"light_scan_max_symbols"`
	QualityFilterMaxSymbols int                           `yaml:"quality_filter_max_symbols"`
	SetupScanMaxSymbols     int                           `yaml:"setup_scan_max_symbols"`
	CoreSymbols             []string                      `yaml:"core_symbols"`
	ForceIncludeSymbols     []string                      `yaml:"force_include_symbols"`
	Blacklist               []string                      `yaml:"blacklist"`
	ExternalSignalWatchlist ExternalSignalWatchlistConfig `yaml:"external_signal_watchlist"`
	Filters                 UniverseFiltersConfig         `yaml:"filters"`
}

// ExternalSignalWatchlistConfig controls external signal TTL.
type ExternalSignalWatchlistConfig struct {
	Enabled  bool `yaml:"enabled"`
	TTLHours int  `yaml:"ttl_hours"`
}

// UniverseFiltersConfig contains filtering thresholds.
type UniverseFiltersConfig struct {
	Min24hVolumeUSD   float64 `yaml:"min_24h_volume_usd"`
	MaxSpreadBps      float64 `yaml:"max_spread_bps"`
	MinATRHealthScore float64 `yaml:"min_atr_health_score"`
}

// StrategyConfig contains strategy settings.
type StrategyConfig struct {
	Enabled               bool             `yaml:"enabled"`
	Timeframes            TimeframesConfig `yaml:"timeframes"`
	Indicators            IndicatorsConfig `yaml:"indicators"`
	Regime                RegimeConfig     `yaml:"regime"`
	EntryTypes            []string         `yaml:"entry_types"`
	LimitRetestTTLMinutes int              `yaml:"limit_retest_ttl_minutes"`
}

// TimeframesConfig maps timeframe roles.
type TimeframesConfig struct {
	Context   string `yaml:"context"`
	Setup     string `yaml:"setup"`
	Execution string `yaml:"execution"`
}

// IndicatorsConfig contains indicator parameters.
type IndicatorsConfig struct {
	ATRPeriod                  int     `yaml:"atr_period"`
	EMA200Period               int     `yaml:"ema200_period"`
	VolumeSMAPeriod            int     `yaml:"volume_sma_period"`
	RangeCandles               int     `yaml:"range_candles"`
	MinVolumeRatio             float64 `yaml:"min_volume_ratio"`
	MaxBreakoutExtensionATR    float64 `yaml:"max_breakout_extension_atr"`
	MaxDistanceFromBreakoutATR float64 `yaml:"max_distance_from_breakout_atr"`
	MinRR                      float64 `yaml:"min_rr"`
	ExpectedMoveCostMultiplier float64 `yaml:"expected_move_cost_multiplier"`
}

// RegimeConfig contains regime detection thresholds.
type RegimeConfig struct {
	TrendUpMinDistanceFromEMAPct   float64 `yaml:"trend_up_min_distance_from_ema_pct"`
	TrendDownMaxDistanceFromEMAPct float64 `yaml:"trend_down_max_distance_from_ema_pct"`
	RangeMaxDistanceFromEMAPct     float64 `yaml:"range_max_distance_from_ema_pct"`
}

// LLMRoutingConfig controls dynamic LLM candidate routing.
type LLMRoutingConfig struct {
	Mode                      string  `yaml:"mode"`
	MinCandidateScore         float64 `yaml:"min_candidate_score"`
	MaxCallsPerCycle          int     `yaml:"max_calls_per_cycle"`
	MaxCallsPerDay            int     `yaml:"max_calls_per_day"`
	MaxCostUSDPerDay          float64 `yaml:"max_cost_usd_per_day"`
	RequireExecutionOk        bool    `yaml:"require_execution_ok"`
	RequireLiquidityOk        bool    `yaml:"require_liquidity_ok"`
	HardCapCandidatesPerCycle int     `yaml:"hard_cap_candidates_per_cycle"`
}

// LLMConfig contains LLM provider settings.
type LLMConfig struct {
	Enabled        bool            `yaml:"enabled"`
	Provider       string          `yaml:"provider"`
	BaseURL        string          `yaml:"base_url"`
	Model          string          `yaml:"model"`
	Temperature    float64         `yaml:"temperature"`
	TimeoutSeconds int             `yaml:"timeout_seconds"`
	MaxTokens      int             `yaml:"max_tokens"`
	Budget         LLMBudgetConfig `yaml:"budget"`
}

// LLMBudgetConfig contains LLM budget controls.
type LLMBudgetConfig struct {
	MaxCostUSDPerDay float64 `yaml:"max_cost_usd_per_day"`
	FallbackDecision string  `yaml:"fallback_decision"`
}

// LLMReviewConfig controls the LLM Reviewer (Phase 6) layer.
type LLMReviewConfig struct {
	Mode             string  `yaml:"mode"`
	Provider         string  `yaml:"provider"`
	Model            string  `yaml:"model"`
	BaseURL          string  `yaml:"base_url"`
	TimeoutSeconds   int     `yaml:"timeout_seconds"`
	MaxInputTokens   int     `yaml:"max_input_tokens"`
	MaxOutputTokens  int     `yaml:"max_output_tokens"`
	DailyCostCapUSD  float64 `yaml:"daily_cost_cap_usd"`
	PromptVersion    string  `yaml:"prompt_version"`
	LogRawResponses  bool    `yaml:"log_raw_responses"`
}

// PortfolioRiskConfig contains portfolio-level risk limits.
type PortfolioRiskConfig struct {
	MaxOpenPositions          int            `yaml:"max_open_positions"`
	MaxNewPositionsPerCycle   int            `yaml:"max_new_positions_per_cycle"`
	MaxTotalExposureUSD       float64        `yaml:"max_total_exposure_usd"`
	MaxTotalMarginUsedPct     float64        `yaml:"max_total_margin_used_pct"`
	MaxRiskPerTradePct        float64        `yaml:"max_risk_per_trade_pct"`
	MaxDailyLossPct           float64        `yaml:"max_daily_loss_pct"`
	MaxSameDirectionPositions int            `yaml:"max_same_direction_positions"`
	MaxCorrelatedAltPositions int            `yaml:"max_correlated_alt_positions"`
	MaxPerSymbolPosition      int            `yaml:"max_per_symbol_position"`
	MarginPerTradeUSD         float64        `yaml:"margin_per_trade_usd"`
	MaxLeverage               float64        `yaml:"max_leverage"`
	MinNotionalUSD            float64        `yaml:"min_notional_usd"`
	CooldownAfterLosses       CooldownConfig `yaml:"cooldown_after_losses"`
}

// CooldownConfig controls post-loss cooldown.
type CooldownConfig struct {
	Enabled           bool `yaml:"enabled"`
	ConsecutiveLosses int  `yaml:"consecutive_losses"`
	CooldownMinutes   int  `yaml:"cooldown_minutes"`
}

// SizingConfig contains position sizing parameters.
type SizingConfig struct {
	Method             string  `yaml:"method"`
	MarginPerTradeUSD  float64 `yaml:"margin_per_trade_usd"`
	MaxLeverage        float64 `yaml:"max_leverage"`
	MaxRiskPerTradePct float64 `yaml:"max_risk_per_trade_pct"`
}

// RiskConfig contains deterministic risk engine parameters (Phase 4).
type RiskConfig struct {
	BaseRiskPerTradePct    float64 `yaml:"base_risk_per_trade_pct"`
	MaxRiskPerTradePct     float64 `yaml:"max_risk_per_trade_pct"`
	MaxLeverage            float64 `yaml:"max_leverage"`
	PreferredLeverage      float64 `yaml:"preferred_leverage"`
	MinRR                  float64 `yaml:"min_rr"`
	MaxOpenPositions       int     `yaml:"max_open_positions"`
	MaxCorrelatedPositions int     `yaml:"max_correlated_positions"`
	DailyMaxLossPct        float64 `yaml:"daily_max_loss_pct"`
	WeeklyMaxLossPct       float64 `yaml:"weekly_max_loss_pct"`
	MinRiskPerTradePct     float64 `yaml:"min_risk_per_trade_pct"`
	RiskConfigVersion      string  `yaml:"risk_config_version"`
}

// AlertsConfig contains alert settings.
type AlertsConfig struct {
	Enabled  bool           `yaml:"enabled"`
	Provider string         `yaml:"provider"`
	Telegram TelegramConfig `yaml:"telegram"`
	Webhook  WebhookConfig  `yaml:"webhook"`
	Events   []string       `yaml:"events"`
}

// TelegramConfig contains Telegram bot settings.
type TelegramConfig struct {
	BotToken string `yaml:"bot_token"`
	ChatID   string `yaml:"chat_id"`
}

// WebhookConfig contains generic webhook settings.
type WebhookConfig struct {
	URL string `yaml:"url"`
}

// BacktestConfig contains backtest defaults.
type BacktestConfig struct {
	DefaultFrom string `yaml:"default_from"`
	DefaultTo   string `yaml:"default_to"`
	LLMMode     string `yaml:"llm_mode"`
}

var envVarRegex = regexp.MustCompile(`\$\{([^}]+)\}`)

func substituteEnvVars(data []byte) ([]byte, error) {
	var unresolved []string
	result := envVarRegex.ReplaceAllFunc(data, func(match []byte) []byte {
		name := string(match[2 : len(match)-1]) // strip ${ and }
		if val, ok := os.LookupEnv(name); ok {
			return []byte(val)
		}
		unresolved = append(unresolved, name)
		return match
	})
	if len(unresolved) > 0 {
		return nil, fmt.Errorf("unresolved environment variable(s): %s", strings.Join(unresolved, ", "))
	}
	return result, nil
}

// LoadConfig reads a YAML config file and validates it.
func LoadConfig(path string) (*UserConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}
	data, err = substituteEnvVars(data)
	if err != nil {
		return nil, fmt.Errorf("substitute env vars: %w", err)
	}

	var cfg UserConfig
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	return &cfg, nil
}

// Validate checks all config sections for correctness.
func (c *UserConfig) Validate() error {
	if err := c.App.validate(); err != nil {
		return err
	}
	// Mode enforcement is handled by ResolveMode at startup (BOTSURV_MODE + LIVE_CONFIRMED env vars).
	if c.Database.Driver != "postgres" {
		return errors.New("database.driver must be 'postgres'")
	}
	if c.Database.DSN == "" {
		return errors.New("database.dsn is required")
	}
	if err := c.MarketData.validate(); err != nil {
		return err
	}
	if err := c.DataValidation.Validate(); err != nil {
		return fmt.Errorf("data_validation: %w", err)
	}
	if err := c.HardBlocks.Validate(); err != nil {
		return fmt.Errorf("hard_blocks: %w", err)
	}
	if err := c.IndicatorEngine.Validate(); err != nil {
		return fmt.Errorf("indicator_engine: %w", err)
	}
	if err := c.MarketRegime.Validate(); err != nil {
		return fmt.Errorf("market_regime: %w", err)
	}
	if err := c.Scoring.Validate(); err != nil {
		return fmt.Errorf("scoring: %w", err)
	}
	if c.Broker.Provider != "paper" {
		return errors.New("phase 5 only supports paper broker; live broker not yet implemented")
	}
	if err := c.Broker.Paper.validate(); err != nil {
		return err
	}
	if c.Sizing.MaxLeverage > 0 && c.Broker.Paper.DefaultLeverage > 0 {
		if c.Sizing.MaxLeverage != c.Broker.Paper.DefaultLeverage {
			return fmt.Errorf("sizing.max_leverage (%.1f) must equal broker.paper.default_leverage (%.1f)",
				c.Sizing.MaxLeverage, c.Broker.Paper.DefaultLeverage)
		}
	}
	if c.ComputeTargetNotional() <= 0 {
		return errors.New("target notional must be > 0: set sizing.margin_per_trade_usd and sizing.max_leverage (or portfolio_risk equivalents)")
	}
	if err := c.Strategy.validate(); err != nil {
		return err
	}
	if err := c.LLMRouting.Validate(); err != nil {
		return fmt.Errorf("llm_routing: %w", err)
	}
	if err := c.PortfolioRisk.Validate(); err != nil {
		return fmt.Errorf("portfolio_risk: %w", err)
	}
	if err := c.Risk.Validate(); err != nil {
		return fmt.Errorf("risk: %w", err)
	}
	if err := c.LLM.Validate(); err != nil {
		return fmt.Errorf("llm: %w", err)
	}
	if err := c.LLMReview.Validate(); err != nil {
		return fmt.Errorf("llm_review: %w", err)
	}
	if err := c.Alerts.validate(); err != nil {
		return err
	}
	if err := c.Backtest.validate(); err != nil {
		return err
	}
	return nil
}

func (c AppConfig) validate() error {
	mode := BotMode(strings.ToLower(c.Mode))
	if c.Mode != "" && !mode.IsValid() {
		return fmt.Errorf("app.mode must be one of: %v", ValidModes())
	}
	if c.LogLevel != "" && c.LogLevel != "debug" && c.LogLevel != "info" && c.LogLevel != "warn" && c.LogLevel != "error" {
		return errors.New("app.log_level must be one of: debug, info, warn, error")
	}
	if c.CycleIntervalSeconds <= 0 {
		return errors.New("app.cycle_interval_seconds must be > 0")
	}
	if c.Timezone != "" {
		if _, err := time.LoadLocation(c.Timezone); err != nil {
			return fmt.Errorf("app.timezone invalid: %w", err)
		}
	}
	return nil
}

func (c MarketDataConfig) validate() error {
	if c.WSURL == "" {
		return errors.New("market_data.ws_url is required")
	}
	if c.RESTURL == "" {
		return errors.New("market_data.rest_url is required")
	}
	if c.StaleDataThresholdSeconds <= 0 {
		return errors.New("market_data.stale_data_threshold_seconds must be > 0")
	}
	if c.ReconnectIntervalSeconds <= 0 {
		return errors.New("market_data.reconnect_interval_seconds must be > 0")
	}
	if c.BackfillCandles < 0 {
		return errors.New("market_data.backfill_candles must be >= 0")
	}
	if c.StartTimeoutSeconds < 0 {
		return errors.New("market_data.start_timeout_seconds must be >= 0")
	}
	if len(c.Timeframes) == 0 {
		return errors.New("market_data.timeframes must not be empty")
	}
	validTimeframes := map[string]bool{"1m": true, "5m": true, "15m": true, "30m": true, "1H": true, "4H": true, "1d": true}
	for _, tf := range c.Timeframes {
		if !validTimeframes[tf] {
			return fmt.Errorf("market_data.timeframes contains invalid timeframe: %s", tf)
		}
	}
	return nil
}

func (c PaperConfig) validate() error {
	if c.StartingBalanceUSD <= 0 {
		return errors.New("broker.paper.starting_balance_usd must be > 0")
	}
	if c.FeeMakerBps < 0 || c.FeeTakerBps < 0 {
		return errors.New("broker.paper fee bps must be >= 0")
	}
	if c.SlippageBps < 0 {
		return errors.New("broker.paper.slippage_bps must be >= 0")
	}
	if c.DefaultLeverage <= 0 {
		return errors.New("broker.paper.default_leverage must be > 0")
	}
	return nil
}

func (c StrategyConfig) validate() error {
	if !c.Enabled {
		return nil
	}
	if c.Indicators.ATRPeriod <= 0 {
		return errors.New("strategy.indicators.atr_period must be > 0")
	}
	if c.Indicators.EMA200Period <= 0 {
		return errors.New("strategy.indicators.ema200_period must be > 0")
	}
	if c.Indicators.VolumeSMAPeriod <= 0 {
		return errors.New("strategy.indicators.volume_sma_period must be > 0")
	}
	if c.Indicators.RangeCandles <= 0 {
		return errors.New("strategy.indicators.range_candles must be > 0")
	}
	if c.Indicators.MinRR <= 0 {
		return errors.New("strategy.indicators.min_rr must be > 0")
	}
	if c.LimitRetestTTLMinutes < 0 {
		return errors.New("strategy.limit_retest_ttl_minutes must be >= 0")
	}
	return nil
}

func (c AlertsConfig) validate() error {
	if !c.Enabled {
		return nil
	}
	if c.Provider != "telegram" && c.Provider != "webhook" {
		return errors.New("alerts.provider must be 'telegram' or 'webhook'")
	}
	return nil
}

func (c BacktestConfig) validate() error {
	if c.LLMMode != "" && c.LLMMode != "mock" && c.LLMMode != "cached" && c.LLMMode != "live" {
		return errors.New("backtest.llm_mode must be one of: mock, cached, live")
	}
	return nil
}

// Validate checks data validation config.
func (c *DataValidationConfig) Validate() error {
	if c.MinCandles < 0 {
		return errors.New("min_candles must be >= 0")
	}
	for tf, secs := range c.MaxDataAgeSeconds {
		if secs <= 0 {
			return fmt.Errorf("max_data_age_seconds[%s] must be > 0", tf)
		}
	}
	return nil
}

// Validate checks hard block config.
func (c *HardBlocksConfig) Validate() error {
	if c.MaxSpreadPct < 0 {
		return errors.New("max_spread_pct must be >= 0")
	}
	if c.MaxFundingAbsPct < 0 {
		return errors.New("max_funding_abs_pct must be >= 0")
	}
	if c.DailyMaxLossPct < 0 {
		return errors.New("daily_max_loss_pct must be >= 0")
	}
	if c.MaxConsecutiveLosses < 0 {
		return errors.New("max_consecutive_losses must be >= 0")
	}
	if c.CooldownAfterLossMin < 0 {
		return errors.New("cooldown_after_loss_min must be >= 0")
	}
	if c.CooldownAfterWinMin < 0 {
		return errors.New("cooldown_after_win_min must be >= 0")
	}
	return nil
}

// Validate checks indicator engine config.
func (c *IndicatorEngineConfig) Validate() error {
	if c.EMA20Period < 0 || c.EMA50Period < 0 || c.EMA200Period < 0 {
		return errors.New("ema periods must be >= 0")
	}
	if c.RSIPeriod < 0 || c.MACDFastPeriod < 0 || c.MACDSlowPeriod < 0 || c.MACDSignalPeriod < 0 {
		return errors.New("rsi/macd periods must be >= 0")
	}
	if c.ATRPeriod < 0 || c.VolumeMAPeriod < 0 {
		return errors.New("atr/volume_ma periods must be >= 0")
	}
	if c.SwingLookback < 0 || c.RecentSwingCount < 0 {
		return errors.New("swing settings must be >= 0")
	}
	if c.SupportResistanceATRTol < 0 {
		return errors.New("support_resistance_atr_tolerance must be >= 0")
	}
	if c.MACDFastPeriod > 0 && c.MACDSlowPeriod > 0 && c.MACDFastPeriod >= c.MACDSlowPeriod {
		return errors.New("macd_fast_period must be < macd_slow_period")
	}
	return nil
}

// Validate checks market regime config.
func (c *MarketRegimeConfig) Validate() error {
	if c.BTCNearLevelATRBuffer < 0 {
		return errors.New("btc_near_level_atr_buffer must be >= 0")
	}
	if c.BTCDRisingFastPct < 0 {
		return errors.New("btcd_rising_fast_pct must be >= 0")
	}
	if err := c.RelativeStrength.Validate(); err != nil {
		return err
	}
	return nil
}

// Validate checks relative strength config.
func (c *RelativeStrengthConfig) Validate() error {
	if c.NeutralBandPct < 0 {
		return errors.New("neutral_band_pct must be >= 0")
	}
	if c.SmoothedEMAPeriod < 0 {
		return errors.New("smoothed_ema_period must be >= 0")
	}
	return nil
}

// Validate checks scoring config.
func (c *ScoringConfig) Validate() error {
	mode := c.Mode
	if mode == "" {
		mode = "balanced"
	}
	if mode != "balanced" && mode != "aggressive" {
		return errors.New("mode must be 'balanced' or 'aggressive'")
	}
	if err := c.Balanced.Validate(); err != nil {
		return fmt.Errorf("balanced: %w", err)
	}
	if err := c.Aggressive.Validate(); err != nil {
		return fmt.Errorf("aggressive: %w", err)
	}
	return nil
}

// Validate checks scoring threshold consistency.
func (c *ScoringThresholdConfig) Validate() error {
	if c.AllowMarket < 0 || c.AllowMarket > 100 {
		return errors.New("allow_market must be between 0 and 100")
	}
	if c.AllowRetestOnly < 0 || c.AllowRetestOnly > 100 {
		return errors.New("allow_retest_only must be between 0 and 100")
	}
	if c.ReduceSize < 0 || c.ReduceSize > 100 {
		return errors.New("reduce_size must be between 0 and 100")
	}
	if c.AllowMarket < c.AllowRetestOnly || c.AllowRetestOnly < c.ReduceSize {
		return errors.New("thresholds must satisfy allow_market >= allow_retest_only >= reduce_size")
	}
	return nil
}

// Validate checks LLM routing config.
func (c *LLMRoutingConfig) Validate() error {
	if c.Mode != "dynamic" {
		return errors.New("mode must be 'dynamic'")
	}
	if c.MinCandidateScore < 0 || c.MinCandidateScore > 100 {
		return errors.New("min_candidate_score must be between 0 and 100")
	}
	if c.MaxCostUSDPerDay < 0 {
		return errors.New("max_cost_usd_per_day must be >= 0")
	}
	return nil
}

// Validate checks portfolio risk config.
func (c *PortfolioRiskConfig) Validate() error {
	if c.MaxOpenPositions <= 0 {
		return errors.New("max_open_positions must be > 0")
	}
	if c.MaxNewPositionsPerCycle <= 0 {
		return errors.New("max_new_positions_per_cycle must be > 0")
	}
	if c.MaxNewPositionsPerCycle > c.MaxOpenPositions {
		return errors.New("max_new_positions_per_cycle cannot exceed max_open_positions")
	}
	if c.MaxTotalExposureUSD <= 0 {
		return errors.New("max_total_exposure_usd must be > 0")
	}
	if c.MaxTotalMarginUsedPct <= 0 || c.MaxTotalMarginUsedPct > 100 {
		return errors.New("max_total_margin_used_pct must be between 0 and 100")
	}
	if c.MaxRiskPerTradePct <= 0 {
		return errors.New("max_risk_per_trade_pct must be > 0")
	}
	if c.MaxDailyLossPct <= 0 {
		return errors.New("max_daily_loss_pct must be > 0")
	}
	if c.MaxLeverage <= 0 {
		return errors.New("max_leverage must be > 0")
	}
	if c.MinNotionalUSD <= 0 {
		return errors.New("min_notional_usd must be > 0")
	}
	if c.MaxSameDirectionPositions > c.MaxOpenPositions {
		return errors.New("max_same_direction_positions cannot exceed max_open_positions")
	}
	if c.MaxPerSymbolPosition > c.MaxOpenPositions {
		return errors.New("max_per_symbol_position cannot exceed max_open_positions")
	}
	return nil
}

// Validate checks risk config.
func (c *RiskConfig) Validate() error {
	if c.BaseRiskPerTradePct < 0 {
		return errors.New("base_risk_per_trade_pct must be >= 0")
	}
	if c.MaxRiskPerTradePct < 0 {
		return errors.New("max_risk_per_trade_pct must be >= 0")
	}
	if c.MaxLeverage < 0 {
		return errors.New("max_leverage must be >= 0")
	}
	if c.PreferredLeverage < 0 {
		return errors.New("preferred_leverage must be >= 0")
	}
	if c.MinRR < 0 {
		return errors.New("min_rr must be >= 0")
	}
	if c.MaxOpenPositions < 0 {
		return errors.New("max_open_positions must be >= 0")
	}
	if c.MaxCorrelatedPositions < 0 {
		return errors.New("max_correlated_positions must be >= 0")
	}
	if c.DailyMaxLossPct < 0 {
		return errors.New("daily_max_loss_pct must be >= 0")
	}
	if c.WeeklyMaxLossPct < 0 {
		return errors.New("weekly_max_loss_pct must be >= 0")
	}
	if c.MinRiskPerTradePct < 0 {
		return errors.New("min_risk_per_trade_pct must be >= 0")
	}
	return nil
}

// WithDefaults returns risk config with phase-4 defaults applied.
func (c RiskConfig) WithDefaults() RiskConfig {
	out := c
	if out.BaseRiskPerTradePct <= 0 {
		out.BaseRiskPerTradePct = 0.5
	}
	if out.MaxRiskPerTradePct <= 0 {
		out.MaxRiskPerTradePct = 1.0
	}
	if out.MaxLeverage <= 0 {
		out.MaxLeverage = 5.0
	}
	if out.PreferredLeverage <= 0 {
		out.PreferredLeverage = 3.0
	}
	if out.MinRR <= 0 {
		out.MinRR = 1.4
	}
	if out.MaxOpenPositions <= 0 {
		out.MaxOpenPositions = 2
	}
	if out.MaxCorrelatedPositions <= 0 {
		out.MaxCorrelatedPositions = 1
	}
	if out.DailyMaxLossPct <= 0 {
		out.DailyMaxLossPct = 3.0
	}
	if out.WeeklyMaxLossPct <= 0 {
		out.WeeklyMaxLossPct = 6.0
	}
	if out.MinRiskPerTradePct <= 0 {
		out.MinRiskPerTradePct = 0.1
	}
	if out.RiskConfigVersion == "" {
		out.RiskConfigVersion = "v1.0.0"
	}
	return out
}

// Validate checks LLM config.
func (c *LLMConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.Provider != "openrouter" && c.Provider != "deepseek" {
		return errors.New("provider must be 'openrouter' or 'deepseek'")
	}
	if c.BaseURL == "" {
		return errors.New("base_url is required when LLM is enabled")
	}
	if c.Model == "" {
		return errors.New("model is required when LLM is enabled")
	}
	if c.Provider == "openrouter" && c.Temperature != 0 {
		return errors.New("temperature must be 0 for openrouter")
	}
	if c.TimeoutSeconds <= 0 {
		return errors.New("timeout_seconds must be > 0")
	}
	if c.MaxTokens <= 0 {
		return errors.New("max_tokens must be > 0")
	}
	// Fail-fast API key validation
	switch c.Provider {
	case "openrouter":
		if os.Getenv("OPENROUTER_API_KEY") == "" {
			return errors.New("OPENROUTER_API_KEY environment variable is required when llm.provider=openrouter")
		}
	case "deepseek":
		if os.Getenv("DEEPSEEK_API_KEY") == "" {
			return errors.New("DEEPSEEK_API_KEY environment variable is required when llm.provider=deepseek")
		}
	}
	return nil
}

// Validate checks LLM review config.
func (c *LLMReviewConfig) Validate() error {
	validModes := map[string]bool{"off": true, "audit_only": true, "veto": true, "review": true}
	if c.Mode == "" {
		c.Mode = "off"
	}
	if !validModes[c.Mode] {
		return fmt.Errorf("mode must be one of: off, audit_only, veto, review")
	}
	if c.Mode == "off" {
		return nil
	}
	if c.Provider != "openrouter" && c.Provider != "deepseek" {
		return errors.New("provider must be 'openrouter' or 'deepseek'")
	}
	if c.Model == "" {
		return errors.New("model is required when llm_review.mode is not off")
	}
	if c.TimeoutSeconds <= 0 {
		return errors.New("timeout_seconds must be > 0")
	}
	if c.MaxInputTokens <= 0 {
		c.MaxInputTokens = 2000
	}
	if c.MaxOutputTokens <= 0 {
		c.MaxOutputTokens = 500
	}
	if c.DailyCostCapUSD < 0 {
		return errors.New("daily_cost_cap_usd must be >= 0")
	}
	return nil
}

// WithDefaults returns LLM review config with defaults applied.
func (c LLMReviewConfig) WithDefaults() LLMReviewConfig {
	if c.Mode == "" {
		c.Mode = "off"
	}
	if c.Provider == "" {
		c.Provider = "deepseek"
	}
	if c.TimeoutSeconds <= 0 {
		c.TimeoutSeconds = 8
	}
	if c.MaxInputTokens <= 0 {
		c.MaxInputTokens = 2000
	}
	if c.MaxOutputTokens <= 0 {
		c.MaxOutputTokens = 500
	}
	if c.PromptVersion == "" {
		c.PromptVersion = "v1.0.0"
	}
	return c
}

// LoadEnv loads .env file if present.
func LoadEnv() error {
	if _, err := os.Stat(".env"); os.IsNotExist(err) {
		return nil
	}
	return godotenv.Load(".env")
}

// ComputeTargetNotional calculates the target notional for orderbook depth/slippage
// estimation based on config sizing. It tries sizing config first, then portfolio risk,
// then broker paper default leverage. It returns 0 only if no valid config exists.
func (c *UserConfig) ComputeTargetNotional() float64 {
	margin := c.Sizing.MarginPerTradeUSD
	if margin <= 0 {
		margin = c.PortfolioRisk.MarginPerTradeUSD
	}
	leverage := c.Sizing.MaxLeverage
	if leverage <= 0 {
		leverage = c.PortfolioRisk.MaxLeverage
	}
	if leverage <= 0 {
		leverage = c.Broker.Paper.DefaultLeverage
	}
	if margin > 0 && leverage > 0 {
		return margin * leverage
	}
	return 0
}

// MinCandlesOrDefault returns configured min candles or default 250.
func (c DataValidationConfig) MinCandlesOrDefault() int {
	if c.MinCandles > 0 {
		return c.MinCandles
	}
	return 250
}

// MaxDataAgeSecondsFor returns configured max data age for timeframe or defaults.
func (c DataValidationConfig) MaxDataAgeSecondsFor(timeframe string) int {
	if secs, ok := c.MaxDataAgeSeconds[timeframe]; ok && secs > 0 {
		return secs
	}
	switch timeframe {
	case "1m":
		return 30
	case "5m":
		return 3600
	case "15m":
		return 1200
	case "1H":
		return 4500
	case "4H":
		return 18000
	default:
		return 180
	}
}

// MaxSpreadPctOrDefault returns configured spread threshold or default 0.15%.
func (c HardBlocksConfig) MaxSpreadPctOrDefault() float64 {
	if c.MaxSpreadPct > 0 {
		return c.MaxSpreadPct
	}
	return 0.15
}

// MaxFundingAbsPctOrDefault returns configured funding threshold or default 0.5%.
func (c HardBlocksConfig) MaxFundingAbsPctOrDefault() float64 {
	if c.MaxFundingAbsPct > 0 {
		return c.MaxFundingAbsPct
	}
	return 0.5
}

// BTCFlashCrash5mPctOrDefault returns configured BTC crash threshold or default -2.5%.
func (c HardBlocksConfig) BTCFlashCrash5mPctOrDefault() float64 {
	if c.BTCFlashCrash5mPct != 0 {
		return c.BTCFlashCrash5mPct
	}
	return -2.5
}

// DailyMaxLossPctOrDefault returns configured daily max loss threshold or default 3.0%.
func (c HardBlocksConfig) DailyMaxLossPctOrDefault() float64 {
	if c.DailyMaxLossPct > 0 {
		return c.DailyMaxLossPct
	}
	return 3.0
}

// MaxConsecutiveLossesOrDefault returns configured consecutive loss cap or default 3.
func (c HardBlocksConfig) MaxConsecutiveLossesOrDefault() int {
	if c.MaxConsecutiveLosses > 0 {
		return c.MaxConsecutiveLosses
	}
	return 3
}

// WithDefaults returns indicator-engine config with phase-2 defaults applied.
func (c IndicatorEngineConfig) WithDefaults() IndicatorEngineConfig {
	out := c
	if out.EMA20Period <= 0 {
		out.EMA20Period = 20
	}
	if out.EMA50Period <= 0 {
		out.EMA50Period = 50
	}
	if out.EMA200Period <= 0 {
		out.EMA200Period = 200
	}
	if out.RSIPeriod <= 0 {
		out.RSIPeriod = 14
	}
	if out.MACDFastPeriod <= 0 {
		out.MACDFastPeriod = 12
	}
	if out.MACDSlowPeriod <= 0 {
		out.MACDSlowPeriod = 26
	}
	if out.MACDSignalPeriod <= 0 {
		out.MACDSignalPeriod = 9
	}
	if out.ATRPeriod <= 0 {
		out.ATRPeriod = 14
	}
	if out.VolumeMAPeriod <= 0 {
		out.VolumeMAPeriod = 20
	}
	if out.SwingLookback <= 0 {
		out.SwingLookback = 3
	}
	if out.RecentSwingCount <= 0 {
		out.RecentSwingCount = 5
	}
	if out.SupportResistanceATRTol <= 0 {
		out.SupportResistanceATRTol = 1.0
	}
	return out
}

// WithDefaults returns market-regime config with phase-2 defaults applied.
func (c MarketRegimeConfig) WithDefaults() MarketRegimeConfig {
	out := c
	if out.BTCDumpShortPct == 0 {
		out.BTCDumpShortPct = -2.5
	}
	if out.BTCDumpMediumPct == 0 {
		out.BTCDumpMediumPct = -3.0
	}
	if out.BTCNearLevelATRBuffer <= 0 {
		out.BTCNearLevelATRBuffer = 1.0
	}
	if out.BTCDRisingFastPct <= 0 {
		out.BTCDRisingFastPct = 0.8
	}
	out.RelativeStrength = out.RelativeStrength.WithDefaults()
	return out
}

// WithDefaults returns relative-strength config with phase-2 defaults applied.
func (c RelativeStrengthConfig) WithDefaults() RelativeStrengthConfig {
	out := c
	if out.StrongOutperformPct == 0 {
		out.StrongOutperformPct = 2.0
	}
	if out.StrongUnderperformPct == 0 {
		out.StrongUnderperformPct = -2.0
	}
	if out.NeutralBandPct == 0 {
		out.NeutralBandPct = 0.5
	}
	if out.SmoothedEMAPeriod <= 0 {
		out.SmoothedEMAPeriod = 8
	}
	return out
}

// WithDefaults returns scoring config with phase-3 defaults applied.
func (c ScoringConfig) WithDefaults() ScoringConfig {
	out := c
	if out.Mode == "" {
		out.Mode = "balanced"
	}
	if out.ScoringVersion == "" {
		out.ScoringVersion = "v0.1.0"
	}
	out.Balanced = out.Balanced.withDefaults(75, 65, 55)
	out.Aggressive = out.Aggressive.withDefaults(70, 60, 50)
	return out
}

func (c ScoringThresholdConfig) withDefaults(allowMarket, allowRetest, reduce float64) ScoringThresholdConfig {
	out := c
	if out.AllowMarket <= 0 {
		out.AllowMarket = allowMarket
	}
	if out.AllowRetestOnly <= 0 {
		out.AllowRetestOnly = allowRetest
	}
	if out.ReduceSize <= 0 {
		out.ReduceSize = reduce
	}
	return out
}
