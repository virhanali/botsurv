package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	os.Setenv("OPENROUTER_API_KEY", "test-key")
	os.Setenv("DEEPSEEK_API_KEY", "test-key")
	code := m.Run()
	os.Exit(code)
}

func minimalValidConfig() *UserConfig {
	return &UserConfig{
		App: AppConfig{
			Mode:                 "paper",
			LogLevel:             "info",
			CycleIntervalSeconds: 900,
			Timezone:             "UTC",
		},
		Database: DatabaseConfig{
			Driver: "postgres",
			DSN:    "postgres://test:test@localhost:5432/test?sslmode=disable",
			Pool:   PoolConfig{MaxOpenConns: 10, MaxIdleConns: 5, ConnMaxLifetime: 1800},
		},
		MarketData: MarketDataConfig{
			WSURL:                     "wss://stream.bybit.com/v5/public/linear",
			RESTURL:                   "https://api.bybit.com",
			StaleDataThresholdSeconds: 30,
			ReconnectIntervalSeconds:  5,
			BackfillCandles:           500,
			Timeframes:                []string{"15m", "1H"},
		},
		Broker: BrokerConfig{
			Provider: "paper",
			Paper: PaperConfig{
				StartingBalanceUSD: 1000,
				FeeMakerBps:        2,
				FeeTakerBps:        5.5,
				SlippageBps:        5,
				DefaultLeverage:    5,
			},
		},
		Strategy: StrategyConfig{
			Enabled: true,
			Indicators: IndicatorsConfig{
				ATRPeriod:       14,
				EMA200Period:    200,
				VolumeSMAPeriod: 20,
				RangeCandles:    20,
				MinRR:           2,
			},
			LimitRetestTTLMinutes: 30,
		},
		LLMRouting:    LLMRoutingConfig{Mode: "dynamic", MinCandidateScore: 75, MaxCostUSDPerDay: 3},
		PortfolioRisk: PortfolioRiskConfig{MaxOpenPositions: 3, MaxNewPositionsPerCycle: 2, MaxTotalExposureUSD: 300, MaxTotalMarginUsedPct: 50, MaxRiskPerTradePct: 0.5, MaxDailyLossPct: 3, MaxLeverage: 5, MinNotionalUSD: 5},
		LLM:           LLMConfig{Enabled: false},
		Alerts:        AlertsConfig{Enabled: false},
		Backtest:      BacktestConfig{LLMMode: "mock"},
	}
}

func TestLoadConfig_Success(t *testing.T) {
	cfg, err := LoadConfig("../../configs/paper.yaml")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.App.Mode != "paper" {
		t.Errorf("expected mode paper, got %s", cfg.App.Mode)
	}
	if cfg.Database.Driver != "postgres" {
		t.Errorf("expected postgres driver, got %s", cfg.Database.Driver)
	}
}

func TestLoadConfig_MissingFile(t *testing.T) {
	_, err := LoadConfig("nonexistent.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "bad.yaml")
	_ = os.WriteFile(path, []byte("not: valid: yaml: ["), 0o644)
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid yaml")
	}
}

func TestLoadConfig_UnknownField(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "unknown.yaml")
	content := `app:
  mode: paper
  cycle_interval_seconds: 900
  unknown_field_xyz: 123
database:
  driver: postgres
  dsn: postgres://test:test@localhost:5432/test?sslmode=disable
market_data:
  ws_url: "wss://stream.bybit.com/v5/public/linear"
  rest_url: "https://api.bybit.com"
  stale_data_threshold_seconds: 30
  reconnect_interval_seconds: 5
  backfill_candles: 500
  timeframes:
    - 15m
broker:
  provider: paper
  paper:
    starting_balance_usd: 1000
    fee_maker_bps: 2
    fee_taker_bps: 5
    slippage_bps: 5
    default_leverage: 5
strategy:
  enabled: false
llm_routing:
  mode: dynamic
  min_candidate_score: 75
  max_cost_usd_per_day: 3
portfolio_risk:
  max_open_positions: 3
  max_new_positions_per_cycle: 2
  max_total_exposure_usd: 300
  max_total_margin_used_pct: 50
  max_risk_per_trade_pct: 0.5
  max_daily_loss_pct: 3
  max_leverage: 5
  min_notional_usd: 5
llm:
  enabled: false
alerts:
  enabled: false
backtest:
  llm_mode: mock
`
	_ = os.WriteFile(path, []byte(content), 0o644)
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for unknown YAML field")
	}
	if !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("expected unknown field error, got: %v", err)
	}
}

func TestLoadConfig_EnvSubstitution(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "env.yaml")
	content := `app:
  mode: ${TEST_BOT_MODE}
  cycle_interval_seconds: 900
database:
  driver: postgres
  dsn: postgres://test:test@localhost:5432/test?sslmode=disable
market_data:
  ws_url: "wss://stream.bybit.com/v5/public/linear"
  rest_url: "https://api.bybit.com"
  stale_data_threshold_seconds: 30
  reconnect_interval_seconds: 5
  backfill_candles: 500
  timeframes:
    - 15m
broker:
  provider: paper
  paper:
    starting_balance_usd: 1000
    fee_maker_bps: 2
    fee_taker_bps: 5
    slippage_bps: 5
    default_leverage: 5
strategy:
  enabled: false
llm_routing:
  mode: dynamic
  min_candidate_score: 75
  max_cost_usd_per_day: 3
portfolio_risk:
  max_open_positions: 3
  max_new_positions_per_cycle: 2
  max_total_exposure_usd: 300
  max_total_margin_used_pct: 50
  max_risk_per_trade_pct: 0.5
  max_daily_loss_pct: 3
  max_leverage: 5
  min_notional_usd: 5
sizing:
  method: fixed_margin
  margin_per_trade_usd: 100
  max_leverage: 10
llm:
  enabled: false
alerts:
  enabled: false
backtest:
  llm_mode: mock
`
	_ = os.WriteFile(path, []byte(content), 0o644)
	t.Setenv("TEST_BOT_MODE", "paper")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.App.Mode != "paper" {
		t.Errorf("expected mode paper after env substitution, got %s", cfg.App.Mode)
	}
}

func TestLoadConfig_EnvSubstitutionMissing(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "env.yaml")
	content := `app:
  mode: ${UNKNOWN_BOT_MODE_XYZ}
  cycle_interval_seconds: 900
database:
  driver: postgres
  dsn: postgres://test:test@localhost:5432/test?sslmode=disable
market_data:
  ws_url: "wss://stream.bybit.com/v5/public/linear"
  rest_url: "https://api.bybit.com"
  stale_data_threshold_seconds: 30
  reconnect_interval_seconds: 5
  backfill_candles: 500
  timeframes:
    - 15m
broker:
  provider: paper
  paper:
    starting_balance_usd: 1000
    fee_maker_bps: 2
    fee_taker_bps: 5
    slippage_bps: 5
    default_leverage: 5
strategy:
  enabled: false
llm_routing:
  mode: dynamic
  min_candidate_score: 75
  max_cost_usd_per_day: 3
portfolio_risk:
  max_open_positions: 3
  max_new_positions_per_cycle: 2
  max_total_exposure_usd: 300
  max_total_margin_used_pct: 50
  max_risk_per_trade_pct: 0.5
  max_daily_loss_pct: 3
  max_leverage: 5
  min_notional_usd: 5
llm:
  enabled: false
alerts:
  enabled: false
backtest:
  llm_mode: mock
`
	_ = os.WriteFile(path, []byte(content), 0o644)
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for unresolved env variable")
	}
	if !strings.Contains(err.Error(), "unresolved environment variable(s): UNKNOWN_BOT_MODE_XYZ") {
		t.Fatalf("expected unresolved env error, got: %v", err)
	}
}

func TestConfigValidate_LiveWithoutConfirmation(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.App.Mode = "live"
	cfg.App.LiveConfirmed = false
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for live mode without confirmation")
	}
}

func TestConfigValidate_LiveModeNotImplemented(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.App.Mode = "live"
	cfg.App.LiveConfirmed = true
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error because live mode is not implemented in phase 1")
	}
}

func TestConfigValidate_InvalidMode(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.App.Mode = "invalid"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
}

func TestConfigValidate_RejectsNonPostgresDatabase(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Database.Driver = "mysql"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for non-postgres database driver")
	}
}

func TestConfigValidate_MissingDSN(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Database.DSN = ""
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing DSN")
	}
}

func TestConfigValidate_InvalidLogLevel(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.App.LogLevel = "trace"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid log level")
	}
}

func TestConfigValidate_InvalidTimezone(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.App.Timezone = "Mars/Phobos"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid timezone")
	}
}

func TestConfigValidate_ZeroCycleInterval(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.App.CycleIntervalSeconds = 0
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for zero cycle interval")
	}
}

func TestConfigValidate_MarketDataMissingTimeframes(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.MarketData.Timeframes = []string{}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for empty timeframes")
	}
}

func TestConfigValidate_MarketDataInvalidTimeframe(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.MarketData.Timeframes = []string{"99s"}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid timeframe")
	}
}

func TestConfigValidate_NonPaperBroker(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Broker.Provider = "bybit_live"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for non-paper broker")
	}
}

func TestConfigValidate_PaperZeroBalance(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Broker.Paper.StartingBalanceUSD = 0
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for zero starting balance")
	}
}

func TestConfigValidate_PortfolioRiskInconsistent(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.PortfolioRisk.MaxNewPositionsPerCycle = 10
	cfg.PortfolioRisk.MaxOpenPositions = 3
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error when max_new_positions_per_cycle exceeds max_open_positions")
	}
}

func TestConfigValidate_AlertsInvalidProvider(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Alerts.Enabled = true
	cfg.Alerts.Provider = "sms"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid alerts provider")
	}
}

func TestConfigValidate_BacktestInvalidLLMMode(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Backtest.LLMMode = "auto"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid backtest llm_mode")
	}
}

func TestConfigValidate_StrategyZeroATRPeriod(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Strategy.Indicators.ATRPeriod = 0
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for zero atr_period")
	}
}

func TestLLMRoutingConfigValidate(t *testing.T) {
	valid := LLMRoutingConfig{Mode: "dynamic", MinCandidateScore: 75, MaxCostUSDPerDay: 3}
	if err := valid.Validate(); err != nil {
		t.Errorf("expected valid, got %v", err)
	}

	invalidMode := LLMRoutingConfig{Mode: "fixed", MinCandidateScore: 75, MaxCostUSDPerDay: 3}
	if err := invalidMode.Validate(); err == nil {
		t.Error("expected error for invalid mode")
	}

	invalidScore := LLMRoutingConfig{Mode: "dynamic", MinCandidateScore: 150, MaxCostUSDPerDay: 3}
	if err := invalidScore.Validate(); err == nil {
		t.Error("expected error for score > 100")
	}
}

func TestPortfolioRiskConfigValidate(t *testing.T) {
	valid := PortfolioRiskConfig{
		MaxOpenPositions: 3, MaxNewPositionsPerCycle: 2, MaxTotalExposureUSD: 300,
		MaxTotalMarginUsedPct: 50, MaxRiskPerTradePct: 0.5, MaxDailyLossPct: 3,
		MaxLeverage: 5, MinNotionalUSD: 5,
	}
	if err := valid.Validate(); err != nil {
		t.Errorf("expected valid, got %v", err)
	}

	invalid := PortfolioRiskConfig{MaxOpenPositions: 0}
	if err := invalid.Validate(); err == nil {
		t.Error("expected error for max_open_positions <= 0")
	}
}

func TestLLMConfigValidate(t *testing.T) {
	valid := LLMConfig{Enabled: true, Provider: "openrouter", BaseURL: "https://openrouter.ai/api/v1", Model: "gpt-4o", Temperature: 0, TimeoutSeconds: 30, MaxTokens: 512}
	if err := valid.Validate(); err != nil {
		t.Errorf("expected valid, got %v", err)
	}

	missingModel := LLMConfig{Enabled: true, Provider: "openrouter", BaseURL: "https://openrouter.ai/api/v1", Model: "", Temperature: 0, TimeoutSeconds: 30, MaxTokens: 512}
	if err := missingModel.Validate(); err == nil {
		t.Error("expected error for missing model")
	}

	missingBaseURL := LLMConfig{Enabled: true, Provider: "openrouter", BaseURL: "", Model: "gpt-4o", Temperature: 0, TimeoutSeconds: 30, MaxTokens: 512}
	if err := missingBaseURL.Validate(); err == nil {
		t.Error("expected error for missing base_url")
	}

	disabled := LLMConfig{Enabled: false}
	if err := disabled.Validate(); err != nil {
		t.Errorf("expected no error when disabled, got %v", err)
	}

	deepseekValid := LLMConfig{Enabled: true, Provider: "deepseek", BaseURL: "https://api.deepseek.com", Model: "deepseek-v4-pro", TimeoutSeconds: 30, MaxTokens: 512}
	if err := deepseekValid.Validate(); err != nil {
		t.Errorf("expected deepseek valid, got %v", err)
	}

	invalidProvider := LLMConfig{Enabled: true, Provider: "unknown", BaseURL: "https://example.com", Model: "test", Temperature: 0, TimeoutSeconds: 30, MaxTokens: 512}
	if err := invalidProvider.Validate(); err == nil {
		t.Error("expected error for invalid provider")
	}
}

func TestConfigValidate_TargetNotionalPositive(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Sizing.MarginPerTradeUSD = 0
	cfg.Sizing.MaxLeverage = 0
	cfg.PortfolioRisk.MarginPerTradeUSD = 0
	cfg.PortfolioRisk.MaxLeverage = 0
	// Broker.Paper.DefaultLeverage remains positive from minimalValidConfig,
	// but margin is zero so target notional should still be zero.
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for zero target notional")
	}
	if !strings.Contains(err.Error(), "target notional must be > 0") {
		t.Errorf("expected target notional error, got: %v", err)
	}
}
