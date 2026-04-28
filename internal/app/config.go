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
	App           AppConfig           `yaml:"app"`
	Database      DatabaseConfig      `yaml:"database"`
	MarketData    MarketDataConfig    `yaml:"market_data"`
	Broker        BrokerConfig        `yaml:"broker"`
	Universe      UniverseConfig      `yaml:"universe"`
	Strategy      StrategyConfig      `yaml:"strategy"`
	LLMRouting    LLMRoutingConfig    `yaml:"llm_routing"`
	LLM           LLMConfig           `yaml:"llm"`
	PortfolioRisk PortfolioRiskConfig `yaml:"portfolio_risk"`
	Sizing        SizingConfig        `yaml:"sizing"`
	Alerts        AlertsConfig        `yaml:"alerts"`
	Backtest      BacktestConfig      `yaml:"backtest"`
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
	Timeframes                []string      `yaml:"timeframes"`
	OrderbookDepth            int           `yaml:"orderbook_depth"`
	TradeFlowWindows          []int         `yaml:"trade_flow_windows"`
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
	if c.App.Mode == "live" {
		if !c.App.LiveConfirmed {
			return errors.New("live mode requires app.live_confirmed: true")
		}
		return errors.New("live mode is not implemented in phase 1")
	}
	if c.Database.Driver != "postgres" {
		return errors.New("database.driver must be 'postgres'")
	}
	if c.Database.DSN == "" {
		return errors.New("database.dsn is required")
	}
	if err := c.MarketData.validate(); err != nil {
		return err
	}
	if c.Broker.Provider != "paper" {
		return errors.New("phase 1 only supports paper broker")
	}
	if err := c.Broker.Paper.validate(); err != nil {
		return err
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
	if err := c.LLM.Validate(); err != nil {
		return fmt.Errorf("llm: %w", err)
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
	if c.Mode != "paper" && c.Mode != "live" {
		return errors.New("app.mode must be 'paper' or 'live'")
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

// Validate checks LLM config.
func (c *LLMConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.Provider != "openrouter" {
		return errors.New("provider must be 'openrouter'")
	}
	if c.BaseURL == "" {
		return errors.New("base_url is required when LLM is enabled")
	}
	if c.Model == "" {
		return errors.New("model is required when LLM is enabled")
	}
	if c.Temperature != 0 {
		return errors.New("temperature must be 0")
	}
	if c.TimeoutSeconds <= 0 {
		return errors.New("timeout_seconds must be > 0")
	}
	if c.MaxTokens <= 0 {
		return errors.New("max_tokens must be > 0")
	}
	return nil
}

// LoadEnv loads .env file if present.
func LoadEnv() error {
	if _, err := os.Stat(".env"); os.IsNotExist(err) {
		return nil
	}
	return godotenv.Load(".env")
}
