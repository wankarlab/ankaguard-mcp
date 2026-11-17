package storage

import (
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strings"

	"github.com/sirupsen/logrus"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// MigrationManager handles database migrations
type MigrationManager struct {
	db  *sql.DB
	log *logrus.Logger
}

// NewMigrationManager creates a new migration manager
func NewMigrationManager(db *sql.DB, log *logrus.Logger) *MigrationManager {
	return &MigrationManager{
		db:  db,
		log: log,
	}
}

// RunMigrations executes all pending migrations
func (m *MigrationManager) RunMigrations() error {
	m.log.Info("Running database migrations...")

	// Ensure migrations table exists
	if err := m.ensureMigrationsTable(); err != nil {
		return fmt.Errorf("failed to ensure migrations table: %w", err)
	}

	// Get applied migrations
	applied, err := m.getAppliedMigrations()
	if err != nil {
		return fmt.Errorf("failed to get applied migrations: %w", err)
	}

	// Get available migrations
	available, err := m.getAvailableMigrations()
	if err != nil {
		return fmt.Errorf("failed to get available migrations: %w", err)
	}

	// Find pending migrations
	pending := m.findPendingMigrations(applied, available)

	if len(pending) == 0 {
		m.log.Info("No pending migrations")
		return nil
	}

	m.log.WithField("count", len(pending)).Info("Found pending migrations")

	// Execute pending migrations
	for _, migration := range pending {
		if err := m.executeMigration(migration); err != nil {
			return fmt.Errorf("failed to execute migration %s: %w", migration.Name, err)
		}
	}

	m.log.Info("All migrations completed successfully")
	return nil
}

// ensureMigrationsTable creates the migrations table if it doesn't exist
func (m *MigrationManager) ensureMigrationsTable() error {
	_, err := m.db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	return err
}

// getAppliedMigrations returns list of applied migration versions
func (m *MigrationManager) getAppliedMigrations() (map[int]bool, error) {
	rows, err := m.db.Query("SELECT version FROM schema_migrations")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := make(map[int]bool)
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		applied[version] = true
	}

	return applied, rows.Err()
}

// Migration represents a database migration
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// getAvailableMigrations reads all migration files
func (m *MigrationManager) getAvailableMigrations() ([]Migration, error) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, err
	}

	var migrations []Migration

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		// Parse version from filename (e.g., "001_initial_schema.sql")
		var version int
		var name string
		_, err := fmt.Sscanf(entry.Name(), "%d_%s", &version, &name)
		if err != nil {
			m.log.WithField("file", entry.Name()).Warn("Skipping invalid migration filename")
			continue
		}

		// Read migration SQL
		content, err := migrationsFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("failed to read migration %s: %w", entry.Name(), err)
		}

		migrations = append(migrations, Migration{
			Version: version,
			Name:    strings.TrimSuffix(entry.Name(), ".sql"),
			SQL:     string(content),
		})
	}

	// Sort by version
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	return migrations, nil
}

// findPendingMigrations returns migrations that haven't been applied
func (m *MigrationManager) findPendingMigrations(applied map[int]bool, available []Migration) []Migration {
	var pending []Migration

	for _, migration := range available {
		if !applied[migration.Version] {
			pending = append(pending, migration)
		}
	}

	return pending
}

// executeMigration executes a single migration
func (m *MigrationManager) executeMigration(migration Migration) error {
	m.log.WithFields(logrus.Fields{
		"version": migration.Version,
		"name":    migration.Name,
	}).Info("Executing migration")

	// Begin transaction
	tx, err := m.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Execute migration SQL
	_, err = tx.Exec(migration.SQL)
	if err != nil {
		return fmt.Errorf("failed to execute SQL: %w", err)
	}

	// Record migration (if not already recorded by the migration itself)
	_, err = tx.Exec(`
		INSERT OR IGNORE INTO schema_migrations (version, name) 
		VALUES (?, ?)
	`, migration.Version, migration.Name)
	if err != nil {
		return fmt.Errorf("failed to record migration: %w", err)
	}

	// Commit transaction
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	m.log.WithField("version", migration.Version).Info("Migration completed")
	return nil
}

// GetCurrentVersion returns the current schema version
func (m *MigrationManager) GetCurrentVersion() (int, error) {
	var version int
	err := m.db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version)
	return version, err
}

// RollbackMigration rolls back a specific migration (if rollback SQL is provided)
func (m *MigrationManager) RollbackMigration(version int) error {
	// This is a placeholder for rollback functionality
	// In practice, you'd need separate rollback SQL files
	return fmt.Errorf("rollback not implemented yet")
}
