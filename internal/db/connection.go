package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

// Open creates a database connection based on driver and DSN.
func Open(driver, dsn string, maxOpenConns, maxIdleConns, connMaxLifetimeSec int) (*sql.DB, error) {
	if driver != "postgres" {
		return nil, fmt.Errorf("unsupported database driver %q: only postgres is supported", driver)
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	if maxOpenConns > 0 {
		db.SetMaxOpenConns(maxOpenConns)
	}
	if maxIdleConns > 0 {
		db.SetMaxIdleConns(maxIdleConns)
	}
	if connMaxLifetimeSec > 0 {
		db.SetConnMaxLifetime(time.Duration(connMaxLifetimeSec) * time.Second)
	}

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return db, nil
}
