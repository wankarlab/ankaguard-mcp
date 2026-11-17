package domain

// OrderSide represents the side of an order
type OrderSide string

const (
	OrderSideBuy  OrderSide = "buy"
	OrderSideSell OrderSide = "sell"
)

// OrderType represents the type of an order
type OrderType string

const (
	OrderTypeMarket            OrderType = "market"
	OrderTypeLimit             OrderType = "limit"
	OrderTypeStopLimit         OrderType = "stop_limit"
	OrderTypeStopMarket        OrderType = "stop_market"
	OrderTypeOCO               OrderType = "oco"
	OrderTypeTrailing          OrderType = "trailing_stop"
	OrderTypeIceberg           OrderType = "iceberg"
	OrderTypeTWAP              OrderType = "twap"
	OrderTypePostOnly          OrderType = "post_only"
	OrderTypeReduceOnly        OrderType = "reduce_only"
	OrderTypeBracket           OrderType = "bracket"
	OrderTypeScaled            OrderType = "scaled"
	OrderTypeFillOrKill        OrderType = "fill_or_kill"
	OrderTypeImmediateOrCancel OrderType = "immediate_or_cancel"
)

// OrderStatus represents the status of an order
type OrderStatus string

const (
	OrderStatusPending         OrderStatus = "pending"
	OrderStatusOpen            OrderStatus = "open"
	OrderStatusFilled          OrderStatus = "filled"
	OrderStatusPartiallyFilled OrderStatus = "partially_filled"
	OrderStatusCanceled        OrderStatus = "canceled"
	OrderStatusCancelled       OrderStatus = "cancelled" // Alternative spelling
	OrderStatusRejected        OrderStatus = "rejected"
	OrderStatusExpired         OrderStatus = "expired"
)

// TimeInForce represents the time in force for an order
type TimeInForce string

const (
	TimeInForceGTC TimeInForce = "GTC" // Good Till Canceled
	TimeInForceIOC TimeInForce = "IOC" // Immediate Or Cancel
	TimeInForceFOK TimeInForce = "FOK" // Fill Or Kill
	TimeInForceGTD TimeInForce = "GTD" // Good Till Date
)

// PositionSide represents the side of a position
type PositionSide string

const (
	PositionSideLong  PositionSide = "long"
	PositionSideShort PositionSide = "short"
)

// ExchangeStatus represents the status of an exchange
type ExchangeStatus string

const (
	ExchangeStatusOnline      ExchangeStatus = "online"
	ExchangeStatusOffline     ExchangeStatus = "offline"
	ExchangeStatusMaintenance ExchangeStatus = "maintenance"
	ExchangeStatusError       ExchangeStatus = "error"
)

// ExchangeFeature represents features supported by an exchange
type ExchangeFeature string

const (
	ExchangeFeatureSpot        ExchangeFeature = "spot"
	ExchangeFeatureSpotTrading ExchangeFeature = "spot_trading"
	ExchangeFeatureFutures     ExchangeFeature = "futures"
	ExchangeFeatureMargin      ExchangeFeature = "margin"
	ExchangeFeatureOptions     ExchangeFeature = "options"
	ExchangeFeatureLending     ExchangeFeature = "lending"
	ExchangeFeatureStaking     ExchangeFeature = "staking"
	ExchangeFeatureWebSocket   ExchangeFeature = "websocket"
	ExchangeFeatureOrderBook   ExchangeFeature = "orderbook"
	ExchangeFeatureTrades      ExchangeFeature = "trades"
	ExchangeFeatureCandles     ExchangeFeature = "candles"
)

// StrategyType represents the type of trading strategy
type StrategyType string

const (
	StrategyTypeRSI            StrategyType = "rsi"
	StrategyTypeMACD           StrategyType = "macd"
	StrategyTypeBollinger      StrategyType = "bollinger"
	StrategyTypeGrid           StrategyType = "grid"
	StrategyTypeDCA            StrategyType = "dca"
	StrategyTypeMartingale     StrategyType = "martingale"
	StrategyTypeArbitrage      StrategyType = "arbitrage"
	StrategyTypeMeanReversion  StrategyType = "mean_reversion"
	StrategyTypeTrendFollowing StrategyType = "trend_following"
	StrategyTypeScalping       StrategyType = "scalping"
	StrategyTypeCustom         StrategyType = "custom"
	StrategyTypeML             StrategyType = "machine_learning"
)

// StrategyStatus represents the status of a strategy
type StrategyStatus string

const (
	StrategyStatusInactive StrategyStatus = "inactive"
	StrategyStatusActive   StrategyStatus = "active"
	StrategyStatusPaused   StrategyStatus = "paused"
	StrategyStatusStopped  StrategyStatus = "stopped"
	StrategyStatusError    StrategyStatus = "error"
)

// BacktestStatus represents the status of a backtest
type BacktestStatus string

const (
	BacktestStatusPending   BacktestStatus = "pending"
	BacktestStatusRunning   BacktestStatus = "running"
	BacktestStatusCompleted BacktestStatus = "completed"
	BacktestStatusFailed    BacktestStatus = "failed"
	BacktestStatusCanceled  BacktestStatus = "canceled"
)

// AlertType represents the type of alert
type AlertType string

const (
	AlertTypePrice       AlertType = "price"
	AlertTypeVolume      AlertType = "volume"
	AlertTypeRSI         AlertType = "rsi"
	AlertTypeMACD        AlertType = "macd"
	AlertTypeBollinger   AlertType = "bollinger"
	AlertTypeOrderFilled AlertType = "order_filled"
	AlertTypePositionPnL AlertType = "position_pnl"
	AlertTypeRiskLimit   AlertType = "risk_limit"
	AlertTypeNews        AlertType = "news"
	AlertTypeSentiment   AlertType = "sentiment"
)

// AlertStatus represents the status of an alert
type AlertStatus string

const (
	AlertStatusActive    AlertStatus = "active"
	AlertStatusTriggered AlertStatus = "triggered"
	AlertStatusInactive  AlertStatus = "inactive"
	AlertStatusExpired   AlertStatus = "expired"
)

// MCPToolName represents available MCP tools
type MCPToolName string

const (
	// Balance and Account Tools
	MCPToolGetBalance   MCPToolName = "get_balance"
	MCPToolGetPositions MCPToolName = "get_positions"
	MCPToolGetOrders    MCPToolName = "get_orders"

	// Trading Tools
	MCPToolPlaceOrder    MCPToolName = "place_order"
	MCPToolCancelOrder   MCPToolName = "cancel_order"
	MCPToolClosePosition MCPToolName = "close_position"
	MCPToolSetLeverage   MCPToolName = "set_leverage"

	// Market Data Tools
	MCPToolGetPrice        MCPToolName = "get_price"
	MCPToolGetOHLCV        MCPToolName = "get_ohlcv"
	MCPToolGetOrderBook    MCPToolName = "get_orderbook"
	MCPToolGetTrades       MCPToolName = "get_trades"
	MCPToolGetFundingRates MCPToolName = "get_funding_rates"

	// Technical Analysis Tools
	MCPToolGetRSI       MCPToolName = "get_rsi"
	MCPToolGetMACD      MCPToolName = "get_macd"
	MCPToolGetBollinger MCPToolName = "get_bollinger"
	MCPToolGetEMA       MCPToolName = "get_ema"
	MCPToolGetSMA       MCPToolName = "get_sma"

	// Strategy and Backtesting Tools
	MCPToolBacktestStrategy   MCPToolName = "backtest_strategy"
	MCPToolOptimizeParameters MCPToolName = "optimize_parameters"
	MCPToolCreateStrategy     MCPToolName = "create_strategy"
	MCPToolStartStrategy      MCPToolName = "start_strategy"
	MCPToolStopStrategy       MCPToolName = "stop_strategy"

	// Risk Management Tools
	MCPToolRiskAssessment        MCPToolName = "risk_assessment"
	MCPToolPortfolioRebalance    MCPToolName = "portfolio_rebalance"
	MCPToolCalculatePositionSize MCPToolName = "calculate_position_size"
	MCPToolDrawdownCalculation   MCPToolName = "drawdown_calculation"
	MCPToolSharpeRatio           MCPToolName = "sharpe_ratio"
	MCPToolKellyCriterion        MCPToolName = "kelly_criterion"

	// Market Analysis Tools
	MCPToolNewsSentiment      MCPToolName = "news_sentiment"
	MCPToolSocialSentiment    MCPToolName = "social_sentiment"
	MCPToolCorrelationMatrix  MCPToolName = "correlation_matrix"
	MCPToolVolatilityAnalysis MCPToolName = "volatility_analysis"
)

// WebSocketMessageType represents WebSocket message types
type WebSocketMessageType string

const (
	WSMessageTypePriceUpdate     WebSocketMessageType = "price_update"
	WSMessageTypeOrderUpdate     WebSocketMessageType = "order_update"
	WSMessageTypePositionUpdate  WebSocketMessageType = "position_update"
	WSMessageTypeBalanceUpdate   WebSocketMessageType = "balance_update"
	WSMessageTypeOrderBookUpdate WebSocketMessageType = "orderbook_update"
	WSMessageTypeTradeUpdate     WebSocketMessageType = "trade_update"
	WSMessageTypeAlert           WebSocketMessageType = "alert"
	WSMessageTypeError           WebSocketMessageType = "error"
	WSMessageTypeHeartbeat       WebSocketMessageType = "heartbeat"
	WSMessageTypeMCPRequest      WebSocketMessageType = "mcp_request"
	WSMessageTypeMCPResponse     WebSocketMessageType = "mcp_response"
)
