package ai

import (
	"context"
	"testing"
	"time"

	"github.com/rantipay/trading-mcp/internal/domain"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockAIDecisionRepository is a mock for AIDecisionRepository
type MockAIDecisionRepository struct {
	mock.Mock
}

func (m *MockAIDecisionRepository) Create(ctx context.Context, decision *domain.AIDecision) error {
	args := m.Called(ctx, decision)
	return args.Error(0)
}

func (m *MockAIDecisionRepository) GetByID(ctx context.Context, id string) (*domain.AIDecision, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.AIDecision), args.Error(1)
}

func (m *MockAIDecisionRepository) GetByPositionID(ctx context.Context, positionID string) ([]*domain.AIDecision, error) {
	args := m.Called(ctx, positionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.AIDecision), args.Error(1)
}

func (m *MockAIDecisionRepository) GetByDecisionType(ctx context.Context, decisionType string) ([]*domain.AIDecision, error) {
	args := m.Called(ctx, decisionType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.AIDecision), args.Error(1)
}

func (m *MockAIDecisionRepository) GetByDateRange(ctx context.Context, from, to time.Time) ([]*domain.AIDecision, error) {
	args := m.Called(ctx, from, to)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.AIDecision), args.Error(1)
}

// TestPositionManager_AIIntegration tests the AI optimizer integration
func TestPositionManager_AIIntegration(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	config := &PositionConfig{
		AutoClose:             false, // Don't auto-close for testing
		TrailingStop:          false,
		CheckInterval:         1,
		AIManagement:          true,
		AICheckInterval:       1,
		AIConfidenceThreshold: 0.7,
	}

	pm := NewPositionManager(config, logger)

	// Verify AI optimizer can be set
	assert.NotNil(t, pm)
	assert.Nil(t, pm.aiOptimizer)

	// Create mock optimizer (we'll just verify the setter works)
	mockRepo := new(MockAIDecisionRepository)
	pm.SetAIDecisionRepo(mockRepo)

	assert.NotNil(t, pm.aiDecisionRepo)
}

// TestPositionManager_AIDecisionLogging tests AI decision logging
func TestPositionManager_AIDecisionLogging(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	config := DefaultPositionConfig()
	config.AIManagement = true

	pm := NewPositionManager(config, logger)

	// Setup mock repository
	mockRepo := new(MockAIDecisionRepository)
	mockRepo.On("Create", mock.Anything, mock.AnythingOfType("*domain.AIDecision")).Return(nil)

	pm.SetAIDecisionRepo(mockRepo)

	// Create a test position
	pos := &ManagedPosition{
		ID:         "test-pos-1",
		Symbol:     "DOGE-USDT",
		Side:       "buy",
		EntryPrice: 0.14,
		Size:       100,
		StopLoss:   0.13,
		Exchange:   "kucoin",
		AIManaged:  true,
	}

	// Create market condition
	condition := &MarketCondition{
		Trend:      "bullish",
		Volatility: 0.002,
		RSI:        55.0,
		ATR:        0.002,
		Confidence: 0.8,
	}

	// Test logging AI decision
	ctx := context.Background()
	pm.logAIDecision(ctx, pos, "stop_loss_adjust", 0.13, 0.135, condition, "Test adjustment")

	// Verify Create was called
	mockRepo.AssertCalled(t, "Create", mock.Anything, mock.AnythingOfType("*domain.AIDecision"))
}

// TestManagedPosition_AIFields tests that AI-related fields are properly set
func TestManagedPosition_AIFields(t *testing.T) {
	pos := &ManagedPosition{
		ID:          "test-pos-1",
		Symbol:      "DOGE-USDT",
		Side:        "buy",
		EntryPrice:  0.14,
		Size:        100,
		StopLoss:    0.13,
		Exchange:    "kucoin",
		AIManaged:   true,
		LastAICheck: time.Now(),
	}

	assert.True(t, pos.AIManaged)
	assert.NotZero(t, pos.LastAICheck)
	assert.Equal(t, "kucoin", pos.Exchange)
}

// TestPositionConfig_AISettings tests AI configuration settings
func TestPositionConfig_AISettings(t *testing.T) {
	config := DefaultPositionConfig()

	assert.False(t, config.AIManagement)
	assert.Equal(t, 60, config.AICheckInterval)
	assert.Equal(t, 0.7, config.AIConfidenceThreshold)

	// Test custom config
	customConfig := &PositionConfig{
		AIManagement:          true,
		AICheckInterval:       30,
		AIConfidenceThreshold: 0.8,
	}

	assert.True(t, customConfig.AIManagement)
	assert.Equal(t, 30, customConfig.AICheckInterval)
	assert.Equal(t, 0.8, customConfig.AIConfidenceThreshold)
}
