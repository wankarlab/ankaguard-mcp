package postgres

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sirupsen/logrus"
)

// RunConfigMigrations executes all pending config migrations from the specified directory.
// It tracks applied migrations in a schema_migrations table and executes only pending ones.
func RunConfigMigrations(ctx context.Context, pool *pgxpool.Pool, migrationsDir string, log *logrus.Logger) error {
	if log == nil {
		return fmt.Errorf("logger cannot be nil")
	}
	if pool == nil {
		return fmt.Errorf("pool cannot be nil")
	}

	log.WithField("directory", migrationsDir).Info("Running config migrations")

	// Ensure schema_migrations table exists
	if err := ensureSchemaMigrationsTable(ctx, pool); err != nil {
		err = fmt.Errorf("failed to ensure schema_migrations table: %w", err)
		log.WithError(err).Error("Migration setup failed")
		return err
	}

	// Get applied migrations
	applied, err := getAppliedMigrations(ctx, pool)
	if err != nil {
		err = fmt.Errorf("failed to get applied migrations: %w", err)
		log.WithError(err).Error("Failed to query applied migrations")
		return err
	}

	// Get available migrations from directory
	available, err := getAvailableMigrationsFromDir(migrationsDir)
	if err != nil {
		err = fmt.Errorf("failed to read available migrations: %w", err)
		log.WithError(err).Error("Failed to read migrations directory")
		return err
	}

	// Find pending migrations
	pending := findPendingMigrations(applied, available)

	if len(pending) == 0 {
		log.Info("No pending migrations")
		return nil
	}

	log.WithField("count", len(pending)).Info("Found pending migrations")

	// Execute pending migrations in order
	for _, migration := range pending {
		if err := executeMigration(ctx, pool, migration, log); err != nil {
			err = fmt.Errorf("failed to execute migration %s: %w", migration.Version, err)
			log.WithError(err).Error("Migration execution failed")
			return err
		}
	}

	log.Info("All config migrations completed successfully")
	return nil
}

// ensureSchemaMigrationsTable creates the schema_migrations table if it doesn't exist.
func ensureSchemaMigrationsTable(ctx context.Context, pool *pgxpool.Pool) error {
	sql := `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ DEFAULT NOW()
		)
	`
	_, err := pool.Exec(ctx, sql)
	return err
}

// getAppliedMigrations returns a map of applied migration versions.
func getAppliedMigrations(ctx context.Context, pool *pgxpool.Pool) (map[string]bool, error) {
	rows, err := pool.Query(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		applied[version] = true
	}

	return applied, rows.Err()
}

// Migration represents a database migration.
type Migration struct {
	Version string // e.g., "001_create_parameter_registry"
	Path    string // full path to .up.sql file
	SQL     string
}

// getAvailableMigrationsFromDir reads all .up.sql migration files from the directory.
func getAvailableMigrationsFromDir(dir string) ([]Migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory %s: %w", dir, err)
	}

	var migrations []Migration

	for _, entry := range entries {
		// Only process .up.sql files
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}

		// Extract version (everything before .up.sql)
		version := strings.TrimSuffix(entry.Name(), ".up.sql")
		fullPath := filepath.Join(dir, entry.Name())

		// Read migration SQL
		content, err := os.ReadFile(fullPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read migration %s: %w", entry.Name(), err)
		}

		migrations = append(migrations, Migration{
			Version: version,
			Path:    fullPath,
			SQL:     string(content),
		})
	}

	// Sort alphabetically by version (001_, 002_, 003_, etc.)
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	return migrations, nil
}

// findPendingMigrations returns migrations that haven't been applied yet.
func findPendingMigrations(applied map[string]bool, available []Migration) []Migration {
	var pending []Migration

	for _, migration := range available {
		if !applied[migration.Version] {
			pending = append(pending, migration)
		}
	}

	return pending
}

// executeMigration executes a single migration in a transaction.
func executeMigration(ctx context.Context, pool *pgxpool.Pool, migration Migration, log *logrus.Logger) error {
	log.WithField("version", migration.Version).Info("Executing migration")

	// Begin transaction
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	// Execute migration SQL
	if _, err := tx.Exec(ctx, migration.SQL); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("failed to execute SQL: %w", err)
	}

	// Record migration
	sql := `
		INSERT INTO schema_migrations (version)
		VALUES ($1)
		ON CONFLICT (version) DO NOTHING
	`
	if _, err := tx.Exec(ctx, sql, migration.Version); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("failed to record migration: %w", err)
	}

	// Commit transaction
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	log.WithField("version", migration.Version).Info("Migration completed")
	return nil
}
