package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/broker"
	"github.com/virhan/botsurv/internal/db"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/executor"
	"github.com/virhan/botsurv/internal/llm"
	"github.com/virhan/botsurv/internal/logger"
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
			return runCycle(cmd.Context(), configPath, true)
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

			// Handle graceful shutdown
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

func runCycle(ctx context.Context, configPath string, once bool) error {
	cfg, err := app.LoadConfig(configPath)
	if err != nil {
		return err
	}

	components, cleanup, err := buildComponents(cfg)
	if err != nil {
		return err
	}
	defer cleanup()

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
	scheduler *scheduler.Scheduler
}

func buildComponents(cfg *app.UserConfig) (*components, func(), error) {
	log := logger.New(nil, logger.Level(cfg.App.LogLevel))

	database, err := db.Open(cfg.Database.Driver, cfg.Database.DSN, cfg.Database.Pool.MaxOpenConns, cfg.Database.Pool.MaxIdleConns, cfg.Database.Pool.ConnMaxLifetime)
	if err != nil {
		return nil, nil, fmt.Errorf("open db: %w", err)
	}
	repos := db.NewPostgresRepositories(database)

	pb := broker.NewPaperBroker(cfg.Broker.Paper, log)

	universeScanner := universe.NewScanner(
		cfg.Universe, cfg.Strategy, cfg.LLMRouting,
		cfg.MarketData.RESTURL, nil,
		repos.UniverseRepository, repos.CandleRepository, log,
	)

	screenerSvc := screener.NewScreener(*cfg, universeScanner, nil, log)

	llmClient := llm.NewMockClient(domain.LLMDecision{
		Decision:       "ALLOW_MARKET",
		Confidence:     0.9,
		SizeMultiplier: 1.0,
	}, nil)

	riskEng := risk.NewEngine(*cfg)

	exec := executor.NewExecutor(pb, log)

	mon := monitor.NewMonitor(pb, nil, cfg.PortfolioRisk, log)

	sched := scheduler.NewScheduler(*cfg, screenerSvc, llmClient, riskEng, exec, mon, log)

	cleanup := func() { database.Close() }

	return &components{scheduler: sched}, cleanup, nil
}
