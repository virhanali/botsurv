package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/db"
	"github.com/virhan/botsurv/internal/logger"
	"github.com/virhan/botsurv/internal/universe"
)

func newUniverseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "universe",
		Short: "Universe scanner commands",
	}

	cmd.AddCommand(newUniverseListCmd())
	cmd.AddCommand(newUniverseRefreshCmd())
	cmd.AddCommand(newUniverseAddCmd())
	cmd.AddCommand(newUniverseRemoveCmd())

	return cmd
}

func buildScanner(cfg *app.UserConfig) (*universe.Scanner, func(), error) {
	log := logger.New(nil, logger.Level(cfg.App.LogLevel))
	database, err := db.Open(cfg.Database.Driver, cfg.Database.DSN, cfg.Database.Pool.MaxOpenConns, cfg.Database.Pool.MaxIdleConns, cfg.Database.Pool.ConnMaxLifetime)
	if err != nil {
		return nil, nil, fmt.Errorf("open db: %w", err)
	}
	repos := db.NewPostgresRepositories(database)
	scanner := universe.NewScanner(
		cfg.Universe,
		cfg.Strategy,
		cfg.LLMRouting,
		cfg.MarketData.RESTURL,
		nil, // MarketDataService not started for CLI
		repos.UniverseRepository,
		repos.CandleRepository,
		log,
	)
	cleanup := func() { database.Close() }
	return scanner, cleanup, nil
}

func newUniverseListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List current universe symbols",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig(configPath)
			if err != nil {
				return err
			}
			scanner, cleanup, err := buildScanner(cfg)
			if err != nil {
				return err
			}
			defer cleanup()

			symbols, err := scanner.GetUniverse(cmd.Context())
			if err != nil {
				return err
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "SYMBOL\tSTATUS\tQUOTE\tBLACKLIST\tFORCE\tLIQ_SCORE\tLAST_SCAN")
			for _, s := range symbols {
				fmt.Fprintf(w, "%s\t%s\t%s\t%v\t%v\t%.2f\t%s\n",
					s.Symbol, s.Status, s.QuoteAsset, s.Blacklist, s.ForceInclude,
					s.LiquidityScore, s.LastScanAt.Format("2006-01-02 15:04"))
			}
			w.Flush()
			fmt.Printf("\nTotal: %d symbols\n", len(symbols))
			return nil
		},
	}
}

func newUniverseRefreshCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Run full universe scan (Layer 1 + Layer 2)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig(configPath)
			if err != nil {
				return err
			}
			scanner, cleanup, err := buildScanner(cfg)
			if err != nil {
				return err
			}
			defer cleanup()

			if err := scanner.RefreshUniverse(cmd.Context()); err != nil {
				return err
			}
			fmt.Println("Universe refreshed successfully.")
			return nil
		},
	}
}

func newUniverseAddCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "add [SYMBOL]",
		Short: "Add a symbol to the universe",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !force {
				return fmt.Errorf("use --force to add a symbol (e.g., bot universe add BTCUSDT --force)")
			}
			cfg, err := app.LoadConfig(configPath)
			if err != nil {
				return err
			}
			scanner, cleanup, err := buildScanner(cfg)
			if err != nil {
				return err
			}
			defer cleanup()

			if err := scanner.AddForceInclude(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Printf("Symbol %s added with force_include=true\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Force include symbol")
	return cmd
}

func newUniverseRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove [SYMBOL]",
		Short: "Blacklist a symbol from the universe",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig(configPath)
			if err != nil {
				return err
			}
			scanner, cleanup, err := buildScanner(cfg)
			if err != nil {
				return err
			}
			defer cleanup()

			if err := scanner.RemoveSymbol(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Printf("Symbol %s blacklisted\n", args[0])
			return nil
		},
	}
}
