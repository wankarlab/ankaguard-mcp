package manager

import "errors"

var (
	ErrBotNotFound     = errors.New("bot not found")
	ErrMaxBotsReached  = errors.New("maximum number of bots reached")
	ErrBotNotRunning   = errors.New("bot is not running")
	ErrInvalidExchange = errors.New("invalid exchange")
	ErrTooManyPairs    = errors.New("too many pairs requested, maximum is 10")
)
