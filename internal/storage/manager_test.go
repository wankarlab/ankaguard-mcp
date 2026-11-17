package storage

import (
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewManager(t *testing.T) {
	log := logrus.New()
	log.SetOutput(os.Stdout)

	// Create temp database
	config := &Config{
		DatabasePath:    ":memory:", // Use in-memory database for tests
		MaxOpenConns:    5,
		MaxIdleConns:    2,
		ConnMaxLifetime: 1 * time.Minute,
	}

	manager, err := NewManager(config, log)
	require.NoError(t, err)
	require.NotNil(t, manager)
	defer manager.Close()

	// Verify repositories are initialized
	assert.NotNil(t, manager.TradingStyles)
	assert.NotNil(t, manager.TimeframeCache)
	assert.NotNil(t, manager.TradeHistory)
	assert.NotNil(t, manager.PerformanceMetrics)
}

func TestManagerInitialize(t *testing.T) {
	log := logrus.New()
	log.SetOutput(os.Stdout)

	config := &Config{
		DatabasePath: ":memory:",
	}

	manager, err := NewManager(config, log)
	require.NoError(t, err)
	defer manager.Close()

	// Initialize should run migrations
	err = manager.Initialize()
	assert.NoError(t, err)

	// Verify schema version
	version, err := manager.migrations.GetCurrentVersion()
	assert.NoError(t, err)
	assert.Greater(t, version, 0, "schema version should be > 0 after migrations")
}

func TestManagerHealthCheck(t *testing.T) {
	log := logrus.New()
	log.SetOutput(os.Stdout)

	config := &Config{
		DatabasePath: ":memory:",
	}

	manager, err := NewManager(config, log)
	require.NoError(t, err)
	defer manager.Close()

	err = manager.Initialize()
	require.NoError(t, err)

	// Health check should pass
	err = manager.HealthCheck()
	assert.NoError(t, err)
}

func TestManagerGetStats(t *testing.T) {
	log := logrus.New()
	log.SetOutput(os.Stdout)

	config := &Config{
		DatabasePath: ":memory:",
	}

	manager, err := NewManager(config, log)
	require.NoError(t, err)
	defer manager.Close()

	err = manager.Initialize()
	require.NoError(t, err)

	// Get stats
	stats := manager.GetStats()
	assert.NotNil(t, stats)
	assert.Contains(t, stats, "open_connections")
	assert.Contains(t, stats, "in_use")
	assert.Contains(t, stats, "idle")
}

func TestManagerWithTransaction(t *testing.T) {
	log := logrus.New()
	log.SetOutput(os.Stdout)

	config := &Config{
		DatabasePath: ":memory:",
	}

	manager, err := NewManager(config, log)
	require.NoError(t, err)
	defer manager.Close()

	err = manager.Initialize()
	require.NoError(t, err)

	// Test successful transaction
	err = manager.WithTransaction(func(tx *sql.Tx) error {
		_, err := tx.Exec("SELECT 1")
		return err
	})
	assert.NoError(t, err)

	// Test failed transaction (should rollback)
	err = manager.WithTransaction(func(tx *sql.Tx) error {
		return assert.AnError
	})
	assert.Error(t, err)
}

func TestManagerCleanExpiredCache(t *testing.T) {
	log := logrus.New()
	log.SetOutput(os.Stdout)

	config := &Config{
		DatabasePath: ":memory:",
	}

	manager, err := NewManager(config, log)
	require.NoError(t, err)
	defer manager.Close()

	err = manager.Initialize()
	require.NoError(t, err)

	// Clean expired cache should not error
	err = manager.CleanExpiredCache()
	assert.NoError(t, err)
}

func TestManagerGetCacheStats(t *testing.T) {
	log := logrus.New()
	log.SetOutput(os.Stdout)

	config := &Config{
		DatabasePath: ":memory:",
	}

	manager, err := NewManager(config, log)
	require.NoError(t, err)
	defer manager.Close()

	err = manager.Initialize()
	require.NoError(t, err)

	// Get cache stats
	stats, err := manager.GetCacheStats()
	assert.NoError(t, err)
	assert.NotNil(t, stats)
	assert.Contains(t, stats, "total_entries")
	assert.Contains(t, stats, "active_entries")
}
