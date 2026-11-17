package adapters

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/ai/calendar"
)

// CalendarAdapter wraps the calendar package and exposes a thin API for
// callers (the bot, the Master Agent) to query upcoming impact without
// having to traverse the underlying Calendar directly.
type CalendarAdapter struct {
	log      *logrus.Logger
	calendar *calendar.Calendar
}

func NewCalendarAdapter(log *logrus.Logger, cal *calendar.Calendar) *CalendarAdapter {
	return &CalendarAdapter{log: log, calendar: cal}
}

// AssessNow returns the impact assessment for "now". Wraps Calendar.AssessImpact.
func (c *CalendarAdapter) AssessNow(ctx context.Context) *calendar.ImpactAssessment {
	if c.calendar == nil {
		return nil
	}
	return c.calendar.AssessImpact(time.Now().UTC())
}

// SuggestedSizeMultiplier returns the multiplier (0..1) currently in effect
// or 1.0 if no impact / no calendar.
func (c *CalendarAdapter) SuggestedSizeMultiplier(ctx context.Context) float64 {
	impact := c.AssessNow(ctx)
	if impact == nil || !impact.HasImpact {
		return 1.0
	}
	return impact.SuggestedSizeMultiplier
}

// IsTradeAllowed returns false only when the current impact says PAUSE.
func (c *CalendarAdapter) IsTradeAllowed(ctx context.Context) bool {
	impact := c.AssessNow(ctx)
	if impact == nil {
		return true
	}
	return impact.SuggestedAction != "PAUSE"
}
