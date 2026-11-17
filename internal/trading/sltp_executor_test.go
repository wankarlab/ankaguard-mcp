package trading

import (
	"context"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mock Position Manager for testing
type mockPositionManager struct {
	positions map[string]*Position
	closed    []string
}

func newMockPositionManager() *mockPositionManager {
	return &mockPositionManager{
		positions: make(map[string]*Position),
		closed:    make([]string, 0),
	}
}

func (m *mockPositionManager) OpenPosition(ctx context.Context, pos *Position) error {
	m.positions[pos.ID] = pos
	return nil
}

func (m *mockPositionManager) ClosePosition(ctx context.Context, positionID string, reason string) error {
	m.closed = append(m.closed, positionID)
	delete(m.positions, positionID)
	return nil
}

func (m *mockPositionManager) PartialClosePosition(ctx context.Context, positionID string, sizeToClose float64, reason string) error {
	pos, exists := m.positions[positionID]
	if !exists {
		return nil
	}
	// Reduce position size
	pos.Size -= sizeToClose
	if pos.Size <= 0 {
		// If size becomes zero or negative, close the position
		return m.ClosePosition(ctx, positionID, reason)
	}
	m.positions[positionID] = pos
	return nil
}

func (m *mockPositionManager) UpdatePosition(ctx context.Context, pos *Position) error {
	m.positions[pos.ID] = pos
	return nil
}

func (m *mockPositionManager) GetPosition(ctx context.Context, positionID string) (*Position, error) {
	pos, exists := m.positions[positionID]
	if !exists {
		return nil, nil
	}
	return pos, nil
}

func (m *mockPositionManager) GetOpenPositions(ctx context.Context) ([]*Position, error) {
	positions := make([]*Position, 0, len(m.positions))
	for _, pos := range m.positions {
		positions = append(positions, pos)
	}
	return positions, nil
}

func (m *mockPositionManager) GetOpenPositionsCount(ctx context.Context) int {
	return len(m.positions)
}

func (m *mockPositionManager) GetTotalExposure(ctx context.Context) (float64, error) {
	return 0, nil
}

func (m *mockPositionManager) GetExposureBySymbol(ctx context.Context, symbol string) (float64, error) {
	return 0, nil
}

func (m *mockPositionManager) CheckConflict(ctx context.Context, newPos *Position) (bool, string, error) {
	return false, "", nil
}

func (m *mockPositionManager) GetPositionsByStrategy(ctx context.Context, strategy string) ([]*Position, error) {
	return nil, nil
}

func (m *mockPositionManager) GetPositionHistory(ctx context.Context, limit int) ([]*Position, error) {
	return nil, nil
}

func TestNewSLTPExecutor(t *testing.T) {
	config := &SLTPConfig{
		CheckIntervalMs: 100,
	}
	pm := newMockPositionManager()

	executor := NewSLTPExecutor(config, pm, nil, logrus.New())

	assert.NotNil(t, executor)
	assert.False(t, executor.IsRunning())
}

func TestShouldTriggerStopLoss_Long(t *testing.T) {
	config := &SLTPConfig{}
	pm := newMockPositionManager()
	executor := NewSLTPExecutor(config, pm, nil, logrus.New()).(*sltpExecutor)

	pos := &Position{
		Side:     "long",
		StopLoss: 49000.0,
	}

	// Price above SL - should not trigger
	assert.False(t, executor.shouldTriggerStopLoss(pos, 50000.0))

	// Price at SL - should trigger
	assert.True(t, executor.shouldTriggerStopLoss(pos, 49000.0))

	// Price below SL - should trigger
	assert.True(t, executor.shouldTriggerStopLoss(pos, 48000.0))
}

func TestShouldTriggerStopLoss_Short(t *testing.T) {
	config := &SLTPConfig{}
	pm := newMockPositionManager()
	executor := NewSLTPExecutor(config, pm, nil, logrus.New()).(*sltpExecutor)

	pos := &Position{
		Side:     "short",
		StopLoss: 51000.0,
	}

	// Price below SL - should not trigger
	assert.False(t, executor.shouldTriggerStopLoss(pos, 50000.0))

	// Price at SL - should trigger
	assert.True(t, executor.shouldTriggerStopLoss(pos, 51000.0))

	// Price above SL - should trigger
	assert.True(t, executor.shouldTriggerStopLoss(pos, 52000.0))
}

func TestShouldTriggerTakeProfit_Long(t *testing.T) {
	config := &SLTPConfig{}
	pm := newMockPositionManager()
	executor := NewSLTPExecutor(config, pm, nil, logrus.New()).(*sltpExecutor)

	pos := &Position{
		Side:       "long",
		TakeProfit: 51000.0,
	}

	// Price below TP - should not trigger
	assert.False(t, executor.shouldTriggerTakeProfit(pos, 50000.0))

	// Price at TP - should trigger
	assert.True(t, executor.shouldTriggerTakeProfit(pos, 51000.0))

	// Price above TP - should trigger
	assert.True(t, executor.shouldTriggerTakeProfit(pos, 52000.0))
}

func TestShouldTriggerTakeProfit_Short(t *testing.T) {
	config := &SLTPConfig{}
	pm := newMockPositionManager()
	executor := NewSLTPExecutor(config, pm, nil, logrus.New()).(*sltpExecutor)

	pos := &Position{
		Side:       "short",
		TakeProfit: 49000.0,
	}

	// Price above TP - should not trigger
	assert.False(t, executor.shouldTriggerTakeProfit(pos, 50000.0))

	// Price at TP - should trigger
	assert.True(t, executor.shouldTriggerTakeProfit(pos, 49000.0))

	// Price below TP - should trigger
	assert.True(t, executor.shouldTriggerTakeProfit(pos, 48000.0))
}

func TestMonitorPosition(t *testing.T) {
	config := &SLTPConfig{}
	pm := newMockPositionManager()
	executor := NewSLTPExecutor(config, pm, nil, logrus.New())

	pos := &Position{
		ID:           "test_pos_1",
		Symbol:       "BTC-USDT",
		Side:         "long",
		CurrentPrice: 50000.0,
	}

	err := executor.MonitorPosition(pos)
	require.NoError(t, err)

	// Verify position is being monitored
	exec := executor.(*sltpExecutor)
	exec.trackerMu.RLock()
	_, exists := exec.monitoredPositions[pos.ID]
	exec.trackerMu.RUnlock()

	assert.True(t, exists)
}

func TestRemovePosition(t *testing.T) {
	config := &SLTPConfig{}
	pm := newMockPositionManager()
	executor := NewSLTPExecutor(config, pm, nil, logrus.New())

	pos := &Position{
		ID:           "test_pos_1",
		Symbol:       "BTC-USDT",
		Side:         "long",
		CurrentPrice: 50000.0,
	}

	executor.MonitorPosition(pos)

	err := executor.RemovePosition(pos.ID)
	require.NoError(t, err)

	// Verify position is no longer monitored
	exec := executor.(*sltpExecutor)
	exec.trackerMu.RLock()
	_, exists := exec.monitoredPositions[pos.ID]
	exec.trackerMu.RUnlock()

	assert.False(t, exists)
}

func TestExecuteStopLoss(t *testing.T) {
	config := &SLTPConfig{}
	pm := newMockPositionManager()
	executor := NewSLTPExecutor(config, pm, nil, logrus.New()).(*sltpExecutor)

	pos := &Position{
		ID:     "test_pos_1",
		Symbol: "BTC-USDT",
		Side:   "long",
	}

	pm.positions[pos.ID] = pos

	err := executor.executeStopLoss(context.Background(), pos)
	require.NoError(t, err)

	// Verify position was closed
	assert.Contains(t, pm.closed, pos.ID)
	assert.NotContains(t, pm.positions, pos.ID)
}

func TestExecuteTakeProfit(t *testing.T) {
	config := &SLTPConfig{}
	pm := newMockPositionManager()
	executor := NewSLTPExecutor(config, pm, nil, logrus.New()).(*sltpExecutor)

	pos := &Position{
		ID:     "test_pos_1",
		Symbol: "BTC-USDT",
		Side:   "long",
	}

	pm.positions[pos.ID] = pos

	err := executor.executeTakeProfit(context.Background(), pos)
	require.NoError(t, err)

	// Verify position was closed
	assert.Contains(t, pm.closed, pos.ID)
	assert.NotContains(t, pm.positions, pos.ID)
}

func TestUpdateSLTP(t *testing.T) {
	config := &SLTPConfig{}
	pm := newMockPositionManager()
	executor := NewSLTPExecutor(config, pm, nil, logrus.New())

	pos := &Position{
		ID:           "test_pos_1",
		Symbol:       "BTC-USDT",
		Side:         "long",
		CurrentPrice: 50000.0,
		StopLoss:     49000.0,
		TakeProfit:   51000.0,
	}

	pm.positions[pos.ID] = pos
	executor.MonitorPosition(pos)

	// Update SL/TP
	newSL := 49500.0
	newTP := 51500.0

	err := executor.UpdateSLTP(context.Background(), pos.ID, newSL, newTP)
	require.NoError(t, err)

	// Verify update
	updated := pm.positions[pos.ID]
	assert.Equal(t, newSL, updated.StopLoss)
	assert.Equal(t, newTP, updated.TakeProfit)
}

func TestEnableTrailingStop(t *testing.T) {
	config := &SLTPConfig{}
	pm := newMockPositionManager()
	executor := NewSLTPExecutor(config, pm, nil, logrus.New())

	pos := &Position{
		ID:           "test_pos_1",
		Symbol:       "BTC-USDT",
		Side:         "long",
		CurrentPrice: 50000.0,
	}

	pm.positions[pos.ID] = pos
	executor.MonitorPosition(pos)

	// Enable trailing stop
	distance := 0.5 // 0.5%

	err := executor.EnableTrailingStop(context.Background(), pos.ID, distance)
	require.NoError(t, err)

	// Verify update
	updated := pm.positions[pos.ID]
	assert.True(t, updated.TrailingStop)
	assert.Equal(t, distance, updated.TrailingDistance)
}

func TestMoveToBreakeven(t *testing.T) {
	config := &SLTPConfig{
		BreakevenBufferPct: 0.05,
	}
	pm := newMockPositionManager()
	executor := NewSLTPExecutor(config, pm, nil, logrus.New())

	pos := &Position{
		ID:           "test_pos_1",
		Symbol:       "BTC-USDT",
		Side:         "long",
		EntryPrice:   50000.0,
		CurrentPrice: 50500.0,
		StopLoss:     49000.0,
	}

	pm.positions[pos.ID] = pos
	executor.MonitorPosition(pos)

	err := executor.MoveToBreakeven(context.Background(), pos.ID)
	require.NoError(t, err)

	// Verify SL moved to entry + buffer
	updated := pm.positions[pos.ID]
	expectedSL := 50000.0 * 1.0005 // entry * (1 + 0.05%)
	assert.InDelta(t, expectedSL, updated.StopLoss, 1.0)
	assert.True(t, updated.BreakevenMoved)
}

func TestGetStats(t *testing.T) {
	config := &SLTPConfig{}
	pm := newMockPositionManager()
	executor := NewSLTPExecutor(config, pm, nil, logrus.New()).(*sltpExecutor)

	// Set some stats
	executor.stats.TotalChecks = 100
	executor.stats.StopLossExecutions = 5
	executor.stats.TakeProfitExecutions = 10

	stats := executor.GetStats()

	assert.Equal(t, int64(100), stats.TotalChecks)
	assert.Equal(t, int64(5), stats.StopLossExecutions)
	assert.Equal(t, int64(10), stats.TakeProfitExecutions)
}

func TestStartStop(t *testing.T) {
	config := &SLTPConfig{
		CheckIntervalMs: 50,
	}
	pm := newMockPositionManager()
	executor := NewSLTPExecutor(config, pm, nil, logrus.New())

	// Start
	err := executor.Start(context.Background())
	require.NoError(t, err)
	assert.True(t, executor.IsRunning())

	// Try to start again (should fail)
	err = executor.Start(context.Background())
	assert.Error(t, err)

	// Wait a bit for monitoring loop
	time.Sleep(100 * time.Millisecond)

	// Stop
	err = executor.Stop()
	require.NoError(t, err)
	assert.False(t, executor.IsRunning())

	// Try to stop again (should fail)
	err = executor.Stop()
	assert.Error(t, err)
}

func TestCheckPosition_StopLossTrigger(t *testing.T) {
	config := &SLTPConfig{
		CheckIntervalMs: 50,
	}
	pm := newMockPositionManager()
	executor := NewSLTPExecutor(config, pm, nil, logrus.New()).(*sltpExecutor)

	pos := &Position{
		ID:           "test_pos_1",
		Symbol:       "BTC-USDT",
		Side:         "long",
		EntryPrice:   50000.0,
		CurrentPrice: 48500.0, // Below SL
		StopLoss:     49000.0,
		TakeProfit:   51000.0,
	}

	pm.positions[pos.ID] = pos

	tracker := &positionTracker{
		position:     pos,
		highestPrice: 50000.0,
		lastUpdate:   time.Now(),
	}

	// Check position (should trigger SL)
	executor.checkPosition(context.Background(), tracker)

	// Verify position was closed
	assert.Contains(t, pm.closed, pos.ID)
	assert.Equal(t, int64(1), executor.stats.StopLossExecutions)
}

func TestCheckPosition_TakeProfitTrigger(t *testing.T) {
	config := &SLTPConfig{
		CheckIntervalMs: 50,
	}
	pm := newMockPositionManager()
	executor := NewSLTPExecutor(config, pm, nil, logrus.New()).(*sltpExecutor)

	pos := &Position{
		ID:           "test_pos_1",
		Symbol:       "BTC-USDT",
		Side:         "long",
		EntryPrice:   50000.0,
		CurrentPrice: 51500.0, // Above TP
		StopLoss:     49000.0,
		TakeProfit:   51000.0,
	}

	pm.positions[pos.ID] = pos

	tracker := &positionTracker{
		position:     pos,
		highestPrice: 51500.0,
		lastUpdate:   time.Now(),
	}

	// Check position (should trigger TP)
	executor.checkPosition(context.Background(), tracker)

	// Verify position was closed
	assert.Contains(t, pm.closed, pos.ID)
	assert.Equal(t, int64(1), executor.stats.TakeProfitExecutions)
}
