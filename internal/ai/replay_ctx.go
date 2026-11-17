package ai

import (
	"context"

	"github.com/sirupsen/logrus"
)

type replayCtxKey struct{}

// WithReplay marks ctx as belonging to a historical replay (backtest,
// walk-forward validation, optimizer trial). Decisions made under a replay
// context are virtual: they never reach an exchange, so their per-candle logs
// are demoted to Debug to keep the live server log readable.
func WithReplay(ctx context.Context) context.Context {
	return context.WithValue(ctx, replayCtxKey{}, true)
}

// IsReplay reports whether ctx was marked with WithReplay.
func IsReplay(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(replayCtxKey{}).(bool)
	return v
}

// logAt logs at the requested level, demoting Info/Warn to Debug for replays.
func (de *DecisionEngine) logAt(ctx context.Context, level logrus.Level, fields logrus.Fields, msg string) {
	if IsReplay(ctx) {
		level = logrus.DebugLevel
	}
	de.log.WithFields(fields).Log(level, msg)
}
