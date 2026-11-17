package scalping

import (
	"errors"

	"github.com/rantipay/trading-mcp/internal/scalping/manager"
)

var (
	ErrInvalidPair     = errors.New("invalid trading pair")
	ErrExceedsRiskLimits = errors.New("request exceeds risk limits")
	ErrInsufficientBalance = errors.New("insufficient balance")
	ErrGCTNotReady     = errors.New("GoCryptoTrader not ready")
	ErrGCTNoTickers    = errors.New("GoCryptoTrader has no tickers available")
	ErrInvalidStrategy = errors.New("invalid strategy")
	ErrInvalidAmount   = errors.New("invalid amount")
	ErrInvalidSide     = errors.New("invalid side, must be 'buy' or 'sell'")
	ErrBotAlreadyRunning = errors.New("bot is already running")

	// Delegated to manager package so errors.Is works across the boundary.
	ErrBotNotFound     = manager.ErrBotNotFound
	ErrMaxBotsReached  = manager.ErrMaxBotsReached
	ErrBotNotRunning   = manager.ErrBotNotRunning
	ErrInvalidExchange = manager.ErrInvalidExchange
	ErrTooManyPairs    = manager.ErrTooManyPairs
)
