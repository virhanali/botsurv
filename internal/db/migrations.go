package db

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var migrationFileRegex = regexp.MustCompile(`^(\d+)_.+\.sql$`)

type migrationFile struct {
	name   string
	prefix int
}

// parseMigrationFiles validates and sorts migration filenames.
// Filenames must match NNN_description.sql where NNN is a numeric prefix.
// Duplicate numeric prefixes are rejected.
func parseMigrationFiles(entries []os.DirEntry) ([]migrationFile, error) {
	var files []migrationFile
	seen := make(map[int]string)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		matches := migrationFileRegex.FindStringSubmatch(name)
		if matches == nil {
			return nil, fmt.Errorf("migration filename %q does not match required pattern NNN_description.sql", name)
		}
		prefix, err := strconv.Atoi(matches[1])
		if err != nil {
			return nil, fmt.Errorf("migration filename %q has invalid numeric prefix: %w", name, err)
		}
		if existing, ok := seen[prefix]; ok {
			return nil, fmt.Errorf("duplicate migration numeric prefix %d: %q and %q", prefix, existing, name)
		}
		seen[prefix] = name
		files = append(files, migrationFile{name: name, prefix: prefix})
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].prefix < files[j].prefix
	})
	return files, nil
}

func checksum(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// Migrate runs all SQL migration files in order from migrationsDir.
func Migrate(db *sql.DB, migrationsDir string) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		filename TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`); err != nil {
		return fmt.Errorf("ensure schema_migrations table: %w", err)
	}
	if _, err := db.Exec(`ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS checksum TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("ensure schema_migrations checksum column: %w", err)
	}

	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	files, err := parseMigrationFiles(entries)
	if err != nil {
		return err
	}

	for _, f := range files {
		path := filepath.Join(migrationsDir, f.name)
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", f.name, err)
		}
		cs := checksum(data)

		var appliedChecksum string
		err = db.QueryRow(`SELECT checksum FROM schema_migrations WHERE filename = $1`, f.name).Scan(&appliedChecksum)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("check migration %s: %w", f.name, err)
		}
		if err == nil {
			if appliedChecksum == "" {
				// Backfill checksum for legacy migrations applied before checksum support
				if _, err := db.Exec(`UPDATE schema_migrations SET checksum = $1 WHERE filename = $2`, cs, f.name); err != nil {
					return fmt.Errorf("backfill checksum for migration %s: %w", f.name, err)
				}
				continue
			}
			if appliedChecksum != cs {
				return fmt.Errorf("migration %s has been modified since it was applied (checksum mismatch: expected %s, got %s)", f.name, appliedChecksum, cs)
			}
			continue
		}

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", f.name, err)
		}
		if _, err := tx.Exec(string(data)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("exec migration %s: %w", f.name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (filename, checksum) VALUES ($1, $2)`, f.name, cs); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", f.name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", f.name, err)
		}
	}
	return nil
}
