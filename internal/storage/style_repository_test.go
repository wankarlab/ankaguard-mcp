package storage

import (
	"os"
	"testing"

	"github.com/rantipay/trading-mcp/internal/ai"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestManager(t *testing.T) *Manager {
	log := logrus.New()
	log.SetOutput(os.Stdout)

	// Use file-based temp database instead of :memory: to avoid SQLite connection pooling issues.
	// SQLite :memory: databases with SetMaxOpenConns > 1 can have multiple connections to different
	// in-memory instances, causing table visibility issues across connections.
	tempFile, err := os.CreateTemp("", "storage-test-*.db")
	require.NoError(t, err)
	tempFile.Close()
	t.Cleanup(func() { os.Remove(tempFile.Name()) })

	config := &Config{
		DatabasePath: tempFile.Name(),
	}

	manager, err := NewManager(config, log)
	require.NoError(t, err)

	err = manager.Initialize()
	require.NoError(t, err)

	return manager
}

func TestTradingStyleRepository_Save(t *testing.T) {
	manager := setupTestManager(t)
	defer manager.Close()

	strategy := &ai.Strategy{
		Name: "test-strategy",
		Type: ai.StyleScalping,
		Timeframes: []ai.TimeframeConfig{
			{Interval: "5m", Weight: 0.5},
			{Interval: "15m", Weight: 0.3},
		},
		RequiredTimeframes: []string{"5m"},
		Confidence:         0.70,
	}

	// Save strategy
	err := manager.TradingStyles.Save(strategy)
	assert.NoError(t, err)

	// Load strategy
	loaded, err := manager.TradingStyles.Load("test-strategy")
	assert.NoError(t, err)
	assert.Equal(t, strategy.Name, loaded.Name)
	assert.Equal(t, strategy.Type, loaded.Type)
	assert.Equal(t, len(strategy.Timeframes), len(loaded.Timeframes))
}

func TestTradingStyleRepository_Update(t *testing.T) {
	manager := setupTestManager(t)
	defer manager.Close()

	strategy := &ai.Strategy{
		Name:       "test-strategy",
		Type:       ai.StyleScalping,
		Timeframes: []ai.TimeframeConfig{{Interval: "5m", Weight: 0.5}},
		Confidence: 0.70,
	}

	// Save initial
	err := manager.TradingStyles.Save(strategy)
	require.NoError(t, err)

	// Update
	strategy.Confidence = 0.80
	err = manager.TradingStyles.Save(strategy)
	assert.NoError(t, err)

	// Verify update
	loaded, err := manager.TradingStyles.Load("test-strategy")
	assert.NoError(t, err)
	assert.Equal(t, 0.80, loaded.Confidence)
}

func TestTradingStyleRepository_List(t *testing.T) {
	manager := setupTestManager(t)
	defer manager.Close()

	// Save multiple strategies
	strategies := []*ai.Strategy{
		{Name: "strategy-1", Type: ai.StyleScalping, Timeframes: []ai.TimeframeConfig{{Interval: "5m", Weight: 1.0}}},
		{Name: "strategy-2", Type: ai.StyleDay, Timeframes: []ai.TimeframeConfig{{Interval: "15m", Weight: 1.0}}},
		{Name: "strategy-3", Type: ai.StyleSwing, Timeframes: []ai.TimeframeConfig{{Interval: "1h", Weight: 1.0}}},
	}

	for _, s := range strategies {
		err := manager.TradingStyles.Save(s)
		require.NoError(t, err)
	}

	// List all
	list, err := manager.TradingStyles.List()
	assert.NoError(t, err)
	assert.Len(t, list, 3)
}

func TestTradingStyleRepository_Delete(t *testing.T) {
	manager := setupTestManager(t)
	defer manager.Close()

	strategy := &ai.Strategy{
		Name:       "test-strategy",
		Type:       ai.StyleScalping,
		Timeframes: []ai.TimeframeConfig{{Interval: "5m", Weight: 1.0}},
	}

	// Save
	err := manager.TradingStyles.Save(strategy)
	require.NoError(t, err)

	// Delete
	err = manager.TradingStyles.Delete("test-strategy")
	assert.NoError(t, err)

	// Verify deleted (should not be in list)
	list, err := manager.TradingStyles.List()
	assert.NoError(t, err)
	assert.Len(t, list, 0)
}

func TestTradingStyleRepository_GetByType(t *testing.T) {
	manager := setupTestManager(t)
	defer manager.Close()

	// Save strategies of different types
	strategies := []*ai.Strategy{
		{Name: "scalping-1", Type: ai.StyleScalping, Timeframes: []ai.TimeframeConfig{{Interval: "5m", Weight: 1.0}}},
		{Name: "scalping-2", Type: ai.StyleScalping, Timeframes: []ai.TimeframeConfig{{Interval: "5m", Weight: 1.0}}},
		{Name: "day-1", Type: ai.StyleDay, Timeframes: []ai.TimeframeConfig{{Interval: "15m", Weight: 1.0}}},
	}

	for _, s := range strategies {
		err := manager.TradingStyles.Save(s)
		require.NoError(t, err)
	}

	// Get by type
	scalpingStrategies, err := manager.TradingStyles.GetByType(string(ai.StyleScalping))
	assert.NoError(t, err)
	assert.Len(t, scalpingStrategies, 2)

	dayStrategies, err := manager.TradingStyles.GetByType(string(ai.StyleDay))
	assert.NoError(t, err)
	assert.Len(t, dayStrategies, 1)
}

func TestTradingStyleRepository_LoadNotFound(t *testing.T) {
	manager := setupTestManager(t)
	defer manager.Close()

	// Try to load non-existent strategy
	_, err := manager.TradingStyles.Load("non-existent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}
