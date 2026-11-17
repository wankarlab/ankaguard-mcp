package domain

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

// TradingEngine defines the interface for trading operations
type TradingEngine interface {
	// Order Management
	PlaceOrder(ctx context.Context, req *PlaceOrderRequest) (*Order, error)
	CancelOrder(ctx context.Context, orderID string) error
	GetOrder(ctx context.Context, orderID string) (*Order, error)
	ListOrders(ctx context.Context, filter *OrderFilter) ([]*Order, error)

	// Position Management
	GetPositions(ctx context.Context, userID string) ([]*Position, error)
	ClosePosition(ctx context.Context, positionID string) error

	// Balance Management
	GetBalance(ctx context.Context, userID, exchange string) (*Balance, error)
	GetBalances(ctx context.Context, userID string) ([]*Balance, error)

	// Exchange Management
	ConnectExchange(ctx context.Context, userID string, config *ExchangeConfig) error
	DisconnectExchange(ctx context.Context, userID, exchange string) error
	GetExchangeStatus(ctx context.Context, exchange string) (*ExchangeStatusInfo, error)
}

// MarketDataService defines the interface for market data operations
type MarketDataService interface {
	// Real-time Data
	SubscribePrice(ctx context.Context, symbol string) (<-chan *PriceUpdate, error)
	SubscribeOrderBook(ctx context.Context, symbol string) (<-chan *OrderBook, error)
	SubscribeTrades(ctx context.Context, symbol string) (<-chan *Trade, error)

	// Historical Data
	GetOHLCV(ctx context.Context, req *OHLCVRequest) ([]*OHLCV, error)
	GetTrades(ctx context.Context, req *TradesRequest) ([]*Trade, error)

	// Technical Indicators
	CalculateRSI(ctx context.Context, symbol string, period int) (decimal.Decimal, error)
	CalculateMACD(ctx context.Context, symbol string, fast, slow, signal int) (*MACD, error)
	CalculateBollinger(ctx context.Context, symbol string, period int, stdDev float64) (*BollingerBands, error)
	CalculateEMA(ctx context.Context, symbol string, period int) (decimal.Decimal, error)
	CalculateSMA(ctx context.Context, symbol string, period int) (decimal.Decimal, error)
}

// RiskManager defines the interface for risk management operations
type RiskManager interface {
	// Position Sizing
	CalculatePositionSize(ctx context.Context, req *PositionSizeRequest) (*PositionSize, error)
	ValidateOrder(ctx context.Context, order *PlaceOrderRequest) error

	// Risk Assessment
	GetPortfolioRisk(ctx context.Context, userID string) (*RiskMetrics, error)
	CheckDrawdown(ctx context.Context, userID string) (*DrawdownStatus, error)

	// Risk Controls
	ApplyStopLoss(ctx context.Context, positionID string) error
	ApplyTakeProfit(ctx context.Context, positionID string) error
	RebalancePortfolio(ctx context.Context, userID string) error

	// Kelly Criterion
	CalculateKellyCriterion(ctx context.Context, req *KellyCriterionRequest) (decimal.Decimal, error)
}

// StrategyEngine defines the interface for strategy operations
type StrategyEngine interface {
	// Strategy Management
	CreateStrategy(ctx context.Context, req *CreateStrategyRequest) (*Strategy, error)
	StartStrategy(ctx context.Context, strategyID string) error
	StopStrategy(ctx context.Context, strategyID string) error
	GetStrategy(ctx context.Context, strategyID string) (*Strategy, error)
	ListStrategies(ctx context.Context, userID string) ([]*Strategy, error)

	// Strategy Execution
	ExecuteStrategy(ctx context.Context, strategyID string) error
	OptimizeParameters(ctx context.Context, strategyID string) (*OptimizationResult, error)
}

// BacktestEngine defines the interface for backtesting operations
type BacktestEngine interface {
	// Backtesting
	RunBacktest(ctx context.Context, req *BacktestRequest) (*Backtest, error)
	GetBacktestResult(ctx context.Context, backtestID string) (*Backtest, error)
	ListBacktests(ctx context.Context, userID string) ([]*Backtest, error)

	// Walk-forward Analysis
	RunWalkForward(ctx context.Context, req *WalkForwardRequest) (*WalkForwardResult, error)
}

// PortfolioManager defines the interface for portfolio management
type PortfolioManager interface {
	GetPortfolio(ctx context.Context, userID string) (*Portfolio, error)
	GetPortfolioHistory(ctx context.Context, userID string, from, to time.Time) ([]*PortfolioSnapshot, error)
	CalculatePerformance(ctx context.Context, userID string) (*PerformanceMetrics, error)
	GetAssetAllocation(ctx context.Context, userID string) (*AssetAllocation, error)
}

// AlertManager defines the interface for alert management
type AlertManager interface {
	CreateAlert(ctx context.Context, req *CreateAlertRequest) (*Alert, error)
	GetAlert(ctx context.Context, alertID string) (*Alert, error)
	ListAlerts(ctx context.Context, userID string) ([]*Alert, error)
	UpdateAlert(ctx context.Context, alertID string, req *UpdateAlertRequest) (*Alert, error)
	DeleteAlert(ctx context.Context, alertID string) error
	CheckAlerts(ctx context.Context) error
}

// MCPToolRegistry defines the interface for MCP tool management
type MCPToolRegistry interface {
	RegisterTool(name MCPToolName, tool MCPTool) error
	GetTool(name MCPToolName) (MCPTool, error)
	ListTools() []MCPToolInfo
	ExecuteTool(ctx context.Context, name MCPToolName, params map[string]interface{}) (interface{}, error)
}

// MCPTool defines the interface for individual MCP tools
type MCPTool interface {
	Name() MCPToolName
	Description() string
	Parameters() map[string]interface{}
	Execute(ctx context.Context, params map[string]interface{}) (interface{}, error)
}

// WebSocketManager defines the interface for WebSocket management
type WebSocketManager interface {
	RegisterConnection(userID string, conn WebSocketConnection) error
	UnregisterConnection(userID string) error
	BroadcastToUser(userID string, message *WebSocketMessage) error
	BroadcastToAll(message *WebSocketMessage) error
	GetActiveConnections() int
}

// WebSocketConnection defines the interface for WebSocket connections
type WebSocketConnection interface {
	Send(message *WebSocketMessage) error
	Close() error
	UserID() string
	IsActive() bool
}

// CacheService defines the interface for caching operations
type CacheService interface {
	Get(ctx context.Context, key string) (interface{}, error)
	Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)

	// Pub/Sub operations
	Publish(ctx context.Context, channel string, message interface{}) error
	Subscribe(ctx context.Context, channel string) (<-chan interface{}, error)
}

// Repository interfaces for data access

// UserRepository defines the interface for user data access
type UserRepository interface {
	Create(ctx context.Context, user *User) error
	GetByID(ctx context.Context, id string) (*User, error)
	Update(ctx context.Context, user *User) error
	Delete(ctx context.Context, id string) error
}

// OrderRepository defines the interface for order data access
type OrderRepository interface {
	Create(ctx context.Context, order *Order) error
	GetByID(ctx context.Context, id string) (*Order, error)
	GetByUserID(ctx context.Context, userID string, filter *OrderFilter) ([]*Order, error)
	Update(ctx context.Context, order *Order) error
	Delete(ctx context.Context, id string) error
}

// PositionRepository defines the interface for position data access
type PositionRepository interface {
	Create(ctx context.Context, position *Position) error
	GetByID(ctx context.Context, id string) (*Position, error)
	GetByUserID(ctx context.Context, userID string) ([]*Position, error)
	Update(ctx context.Context, position *Position) error
	Delete(ctx context.Context, id string) error
}

// BalanceRepository defines the interface for balance data access
type BalanceRepository interface {
	Create(ctx context.Context, balance *Balance) error
	GetByUserAndExchange(ctx context.Context, userID, exchangeID string) ([]*Balance, error)
	Update(ctx context.Context, balance *Balance) error
	Delete(ctx context.Context, userID, exchangeID, asset string) error
}

// StrategyRepository defines the interface for strategy data access
type StrategyRepository interface {
	Create(ctx context.Context, strategy *Strategy) error
	GetByID(ctx context.Context, id string) (*Strategy, error)
	GetByUserID(ctx context.Context, userID string) ([]*Strategy, error)
	Update(ctx context.Context, strategy *Strategy) error
	Delete(ctx context.Context, id string) error
}

// BacktestRepository defines the interface for backtest data access
type BacktestRepository interface {
	Create(ctx context.Context, backtest *Backtest) error
	GetByID(ctx context.Context, id string) (*Backtest, error)
	GetByUserID(ctx context.Context, userID string) ([]*Backtest, error)
	Update(ctx context.Context, backtest *Backtest) error
	Delete(ctx context.Context, id string) error
}

// AlertRepository defines the interface for alert data access
type AlertRepository interface {
	Create(ctx context.Context, alert *Alert) error
	GetByID(ctx context.Context, id string) (*Alert, error)
	GetByUserID(ctx context.Context, userID string) ([]*Alert, error)
	GetActiveAlerts(ctx context.Context) ([]*Alert, error)
	Update(ctx context.Context, alert *Alert) error
	Delete(ctx context.Context, id string) error
}

// StopLossRepository defines the interface for stop loss data access
type StopLossRepository interface {
	Create(ctx context.Context, stopLoss *StopLoss) error
	GetByID(ctx context.Context, id string) (*StopLoss, error)
	GetByPositionID(ctx context.Context, positionID string) (*StopLoss, error)
	GetActive(ctx context.Context) ([]*StopLoss, error)
	Update(ctx context.Context, stopLoss *StopLoss) error
	Delete(ctx context.Context, id string) error
	MarkTriggered(ctx context.Context, id string, triggeredAt time.Time) error
}

// TakeProfitRepository defines the interface for take profit data access
type TakeProfitRepository interface {
	Create(ctx context.Context, takeProfit *TakeProfit) error
	GetByID(ctx context.Context, id string) (*TakeProfit, error)
	GetByPositionID(ctx context.Context, positionID string) (*TakeProfit, error)
	GetActive(ctx context.Context) ([]*TakeProfit, error)
	Update(ctx context.Context, takeProfit *TakeProfit) error
	Delete(ctx context.Context, id string) error
}

// ExecutionHistoryRepository defines the interface for execution history data access
type ExecutionHistoryRepository interface {
	Create(ctx context.Context, history *ExecutionHistory) error
	GetByID(ctx context.Context, id string) (*ExecutionHistory, error)
	GetByPositionID(ctx context.Context, positionID string) ([]*ExecutionHistory, error)
	GetByDateRange(ctx context.Context, from, to time.Time) ([]*ExecutionHistory, error)
	GetStats(ctx context.Context, from, to time.Time) (*ExecutionStats, error)
}

// AIDecisionRepository defines the interface for AI decision data access
type AIDecisionRepository interface {
	Create(ctx context.Context, decision *AIDecision) error
	GetByID(ctx context.Context, id string) (*AIDecision, error)
	GetByPositionID(ctx context.Context, positionID string) ([]*AIDecision, error)
	GetByDecisionType(ctx context.Context, decisionType string) ([]*AIDecision, error)
	GetByDateRange(ctx context.Context, from, to time.Time) ([]*AIDecision, error)
}

// ExecutionStats represents statistics for SL/TP executions
type ExecutionStats struct {
	TotalExecutions int             `json:"total_executions"`
	StopLossCount   int             `json:"stop_loss_count"`
	TakeProfitCount int             `json:"take_profit_count"`
	TotalPnL        decimal.Decimal `json:"total_pnl"`
	AveragePnL      decimal.Decimal `json:"average_pnl"`
	SuccessRate     float64         `json:"success_rate"`
	PrematureStops  int             `json:"premature_stops"`
}
