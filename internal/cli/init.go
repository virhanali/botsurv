package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/db"
	"github.com/virhan/botsurv/internal/logger"
)

var migrationsDirFlag string

func newInitCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize the bot (create data dir, run migrations)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig(configPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			log := logger.Default()
			log.Info("initializing bot", map[string]any{"config": configPath})

			database, err := db.Open(cfg.Database.Driver, cfg.Database.DSN, cfg.Database.Pool.MaxOpenConns, cfg.Database.Pool.MaxIdleConns, cfg.Database.Pool.ConnMaxLifetime)
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer database.Close()

			migrationsDir := migrationsDirFlag
			if migrationsDir == "" {
				migrationsDir = filepath.Join(filepath.Dir(configPath), "..", "migrations")
				if _, err := os.Stat(migrationsDir); os.IsNotExist(err) {
					// fallback to project root migrations
					migrationsDir = "migrations"
				}
			}
			if err := db.Migrate(database, migrationsDir); err != nil {
				return fmt.Errorf("run migrations: %w", err)
			}

			log.Info("bot initialized successfully", nil)
			fmt.Println("Bot initialized successfully.")
			return nil
		},
	}
	cmd.Flags().StringVar(&migrationsDirFlag, "migrations-dir", "", "path to migrations directory (default: ../migrations relative to config)")
	return cmd
}
