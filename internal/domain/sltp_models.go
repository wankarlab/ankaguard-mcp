package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// StopLoss represents a stop loss configuration
type StopLoss struct {
	ID            string          `json:"id" db:"id" gorm:"primaryKey"`
	PositionID    string          `json:"position_id" db:"position_id" gorm:"index"`
	Symbol        string          `json:"symbol" db:"symbol"`
	Exchange      string          `json:"exchange" db:"exchange"`
	StopPrice     decimal.Decimal `json:"stop_price" db:"stop_price"`
	TrailingStop  bool            `json:"trailing_stop" db:"trailing_stop"`
	TrailingDelta decimal.Decimal `json:"trailing_delta" db:"trailing_delta"`
	AIManaged     bool            `json:"ai_managed" db:"ai_managed"`
	Status        string          `json:"status" db:"status" gorm:"index"` // "active", "triggered", "cancelled"
	CreatedAt     time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at" db:"updated_at"`
	TriggeredAt   *time.Time      `json:"triggered_at,omitempty" db:"triggered_at"`
}

// TableName specifies the table name for StopLoss
func (StopLoss) TableName() string {
	return "stop_losses"
}

// TakeProfit represents a take profit configuration
type TakeProfit struct {
	ID         string    `json:"id" db:"id" gorm:"primaryKey"`
	PositionID string    `json:"position_id" db:"position_id" gorm:"index"`
	Symbol     string    `json:"symbol" db:"symbol"`
	Exchange   string    `json:"exchange" db:"exchange"`
	Levels     []TPLevel `json:"levels" gorm:"serializer:json"`
	Status     string    `json:"status" db:"status"`
	CreatedAt  time.Time `json:"created_at" db:"created_at"`
	UpdatedAt  time.Time `json:"updated_at" db:"updated_at"`
}

// TableName specifies the table name for TakeProfit
func (TakeProfit) TableName() string {
	return "take_profits"
}

// TPLevel represents a single take profit level
type TPLevel struct {
	Price      decimal.Decimal `json:"price"`
	Percentage decimal.Decimal `json:"percentage"` // % of position to close
	Executed   bool            `json:"executed"`
	ExecutedAt *time.Time      `json:"executed_at,omitempty"`
}

// ExecutionHistory represents the history of SL/TP executions
type ExecutionHistory struct {
	ID             string          `json:"id" db:"id" gorm:"primaryKey"`
	PositionID     string          `json:"position_id" db:"position_id" gorm:"index"`
	Type           string          `json:"type" db:"type"` // "stop_loss", "take_profit"
	Symbol         string          `json:"symbol" db:"symbol"`
	Exchange       string          `json:"exchange" db:"exchange"`
	TriggerPrice   decimal.Decimal `json:"trigger_price" db:"trigger_price"`
	ExecutionPrice decimal.Decimal `json:"execution_price" db:"execution_price"`
	Size           decimal.Decimal `json:"size" db:"size"`
	PnL            decimal.Decimal `json:"pnl" db:"pnl"`
	Reason         string          `json:"reason" db:"reason"`
	ExecutedAt     time.Time       `json:"executed_at" db:"executed_at" gorm:"index"`
}

// TableName specifies the table name for ExecutionHistory
func (ExecutionHistory) TableName() string {
	return "execution_history"
}

// AIDecision represents an AI decision for learning
type AIDecision struct {
	ID              string          `json:"id" db:"id" gorm:"primaryKey"`
	PositionID      string          `json:"position_id" db:"position_id" gorm:"index"`
	DecisionType    string          `json:"decision_type" db:"decision_type" gorm:"index"` // "stop_loss_adjust", "take_profit_set", etc
	BeforeValue     decimal.Decimal `json:"before_value" db:"before_value"`
	AfterValue      decimal.Decimal `json:"after_value" db:"after_value"`
	MarketCondition string          `json:"market_condition" db:"market_condition"` // JSON
	Indicators      string          `json:"indicators" db:"indicators"`             // JSON
	Confidence      decimal.Decimal `json:"confidence" db:"confidence"`
	Outcome         string          `json:"outcome" db:"outcome"` // "success", "premature", "saved_loss"
	CreatedAt       time.Time       `json:"created_at" db:"created_at"`
}

// TableName specifies the table name for AIDecision
func (AIDecision) TableName() string {
	return "ai_decisions"
}
