package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

const schemaMigrationsSQL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	checksum TEXT NOT NULL,
	applied_at TEXT NOT NULL
);
`

var migrationFilename = regexp.MustCompile(`^([0-9]{4})_([a-z0-9_]+)\.sql$`)

type migration struct {
	version  int
	name     string
	checksum string
	sql      string
}

type appliedMigration struct {
	name     string
	checksum string
}

// ApplyMigrations applies all embedded migrations and records their history.
func ApplyMigrations(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("database handle is required")
	}

	migrations, err := loadMigrations()
	if err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.ExecContext(ctx, schemaMigrationsSQL); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	applied, err := loadAppliedMigrations(ctx, tx)
	if err != nil {
		return err
	}
	known := make(map[int]migration, len(migrations))
	for _, migration := range migrations {
		known[migration.version] = migration
	}
	for version := range applied {
		if _, ok := known[version]; !ok {
			return fmt.Errorf("schema version %04d is not known to this binary", version)
		}
	}

	for _, migration := range migrations {
		record, ok := applied[migration.version]
		if ok {
			if record.name != migration.name || record.checksum != migration.checksum {
				return fmt.Errorf("schema migration %04d changed since it was applied", migration.version)
			}
			continue
		}

		if _, err := tx.ExecContext(ctx, migration.sql); err != nil {
			return fmt.Errorf("apply migration %04d_%s: %w", migration.version, migration.name, err)
		}
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO schema_migrations (version, name, checksum, applied_at)
			 VALUES (?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`,
			migration.version,
			migration.name,
			migration.checksum,
		); err != nil {
			return fmt.Errorf("record migration %04d_%s: %w", migration.version, migration.name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	committed = true
	return nil
}

// loadAppliedMigrations reads existing migration history inside the transaction.
func loadAppliedMigrations(ctx context.Context, tx *sql.Tx) (map[int]appliedMigration, error) {
	rows, err := tx.QueryContext(ctx, `SELECT version, name, checksum FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema migrations: %w", err)
	}
	defer rows.Close()

	applied := map[int]appliedMigration{}
	for rows.Next() {
		var version int
		var record appliedMigration
		if err := rows.Scan(&version, &record.name, &record.checksum); err != nil {
			return nil, fmt.Errorf("scan schema migration: %w", err)
		}
		applied[version] = record
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schema migrations: %w", err)
	}
	return applied, nil
}

// loadMigrations reads embedded SQL files and validates their version sequence.
func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations directory: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	var migrations []migration
	for _, entry := range entries {
		if entry.IsDir() {
			return nil, fmt.Errorf("migration directory contains nested directory %q", entry.Name())
		}
		match := migrationFilename.FindStringSubmatch(entry.Name())
		if match == nil {
			return nil, fmt.Errorf("migration filename %q must match NNNN_name.sql", entry.Name())
		}

		version, err := strconv.Atoi(match[1])
		if err != nil {
			return nil, fmt.Errorf("parse migration version %q: %w", entry.Name(), err)
		}
		sqlBytes, err := fs.ReadFile(migrationFS, path.Join("migrations", entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		script := strings.TrimSpace(string(sqlBytes))
		if script == "" {
			return nil, fmt.Errorf("migration %q is empty", entry.Name())
		}
		sum := sha256.Sum256([]byte(script))
		migrations = append(migrations, migration{
			version:  version,
			name:     match[2],
			checksum: hex.EncodeToString(sum[:]),
			sql:      script,
		})
	}
	if len(migrations) == 0 {
		return nil, fmt.Errorf("no migrations found")
	}
	for i, migration := range migrations {
		want := i + 1
		if migration.version != want {
			return nil, fmt.Errorf("migration versions must be contiguous: got %04d, want %04d", migration.version, want)
		}
	}

	return migrations, nil
}
