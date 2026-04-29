package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/broker"
	"github.com/virhan/botsurv/internal/db"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/executor"
	"github.com/virhan/botsurv/internal/llm"
	"github.com/virhan/botsurv/internal/logger"
	"github.com/virhan/botsurv/internal/marketdata"
	"github.com/virhan/botsurv/internal/monitor"
	"github.com/virhan/botsurv/internal/risk"
	"github.com/virhan/botsurv/internal/scheduler"
	"github.com/virhan/botsurv/internal/screener"
	"github.com/virhan/botsurv/internal/universe"
)

func newRunOnceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run-once",
		Short: "Run a single trading cycle",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCycle(cmd.Context(), configPath)
		},
	}
}

func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Run the main trading loop",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig(configPath)
			if err != nil {
				return err
			}

			components, cleanup, err := buildComponents(cfg)
			if err != nil {
				return err
			}
			defer cleanup()

			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()

			// Preflight: universe refresh + market data start.
			if err := preflight(ctx, cfg, components); err != nil {
				return err
			}

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-sigCh
				fmt.Println("\nShutting down...")
				cancel()
			}()

			return components.scheduler.Run(ctx)
		},
	}
}

func runCycle(ctx context.Context, configPath string) error {
	cfg, err := app.LoadConfig(configPath)
	if err != nil {
		return err
	}

	components, cleanup, err := buildComponents(cfg)
	if err != nil {
		return err
	}
	defer cleanup()

	// Preflight: universe refresh + market data start.
	if err := preflight(ctx, cfg, components); err != nil {
		return err
	}

	result, err := components.scheduler.RunOnce(ctx)
	if err != nil {
		return err
	}

	fmt.Printf("Cycle: %s\n", result.CycleID)
	fmt.Printf("Candidates: %d\n", result.Candidates)
	fmt.Printf("LLM Calls: %d\n", result.LLMCalls)
	fmt.Printf("Executions: %d\n", result.Executions)
	fmt.Printf("Skips: %d\n", len(result.Skips))
	for _, skip := range result.Skips {
		fmt.Printf("  - %s: %s\n", skip.Symbol, skip.Reason)
	}

	return nil
}

type components struct {
	scheduler       *scheduler.Scheduler
	mdSvc           marketdata.MarketDataService
	universeScanner *universe.Scanner
}

func buildComponents(cfg *app.UserConfig) (*components, func(), error) {
	log := logger.New(nil, logger.Level(cfg.App.LogLevel))

	database, err := db.Open(cfg.Database.Driver, cfg.Database.DSN, cfg.Database.Pool.MaxOpenConns, cfg.Database.Pool.MaxIdleConns, cfg.Database.Pool.ConnMaxLifetime)
	if err != nil {
		return nil, nil, fmt.Errorf("open db: %w", err)
	}
	repos := db.NewPostgresRepositories(database)

	pb := broker.NewPaperBroker(cfg.Broker.Paper, log)

	// Create Bybit WS market data service
	httpClient := &http.Client{Timeout: 30 * time.Second}
	mdSvc := marketdata.NewBybitWSMarketDataService(cfg.MarketData, repos.CandleRepository, httpClient, log)

	// Scan universe first to get symbols for WS (needed for all_usdt_perpetual mode)
	universeScanner := universe.NewScanner(
		cfg.Universe, cfg.Strategy, cfg.LLMRouting,
		cfg.MarketData.RESTURL, mdSvc,
		repos.UniverseRepository, repos.CandleRepository, log,
	)

	screenerSvc := screener.NewScreener(*cfg, universeScanner, mdSvc, log)

	llmClient := llm.NewMockClient(domain.LLMDecision{
		Decision:       "ALLOW_MARKET",
		Confidence:     0.9,
		SizeMultiplier: 1.0,
	}, nil)

	riskEng := risk.NewEngine(*cfg)

	exec := executor.NewExecutor(pb, log)

	mon := monitor.NewMonitor(pb, mdSvc, cfg.PortfolioRisk, log)

	sched := scheduler.NewScheduler(*cfg, screenerSvc, llmClient, riskEng, exec, mon, mdSvc, log)

	cleanup := func() {
		mdSvc.Stop(context.Background())
		database.Close()
	}

	return &components{
		scheduler:       sched,
		mdSvc:           mdSvc,
		universeScanner: universeScanner,
	}, cleanup, nil
}

// preflight performs network-dependent initialization: universe refresh,
// symbol provisioning, and market data service start. Fails closed for
// bybit_ws if any step fails.
func preflight(ctx context.Context, cfg *app.UserConfig, c *components) error {
	log := logger.New(nil, logger.Level(cfg.App.LogLevel))

	// Refresh universe to populate symbol list
	preflightCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := c.universeScanner.RefreshUniverse(preflightCtx); err != nil {
		log.Warn("universe refresh failed", map[string]any{"error": err.Error()})
		if cfg.MarketData.Provider == "bybit_ws" {
			return fmt.Errorf("universe refresh failed: %w", err)
		}
	}

	// Feed symbols to WS if all_usdt_perpetual mode
	if cfg.MarketData.Symbols.Mode != "explicit" {
		symbols, err := c.universeScanner.GetUniverse(preflightCtx)
		if err != nil {
			return fmt.Errorf("get universe failed: %w", err)
		}
		symbolList := make([]string, 0, len(symbols))
		for _, s := range symbols {
			if !s.Blacklist {
				symbolList = append(symbolList, s.Symbol)
			}
		}
		c.mdSvc.SetSymbols(symbolList)
		log.Info("marketdata symbols set", map[string]any{"count": len(symbolList)})
	}

	// Start market data service (blocks until initial backfill ready).
	if err := c.mdSvc.Start(ctx); err != nil {
		if cfg.MarketData.Provider == "bybit_ws" {
			return fmt.Errorf("market data provider %q failed to start: %w", cfg.MarketData.Provider, err)
		}
		log.Warn("marketdata service failed to start, continuing without live data", map[string]any{
			"provider": cfg.MarketData.Provider,
			"error":    err.Error(),
		})
	}

	return nil
}
