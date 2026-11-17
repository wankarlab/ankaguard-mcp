package calendar

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

type Calendar struct {
	log    *logrus.Logger
	events []*Event
	mu     sync.RWMutex
}

func NewCalendar(log *logrus.Logger) *Calendar {
	return &Calendar{
		log:    log,
		events: make([]*Event, 0),
	}
}

func severityRank(s Severity) int {
	switch s {
	case SeverityCritical:
		return 4
	case SeverityHigh:
		return 3
	case SeverityMedium:
		return 2
	case SeverityLow:
		return 1
	default:
		return 0
	}
}

func (c *Calendar) AddEvent(event *Event) {
	if event == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	for i, e := range c.events {
		if e.ID == event.ID {
			c.events[i] = event
			c.sortLocked()
			if c.log != nil {
				c.log.WithField("event_id", event.ID).Debug("calendar: event replaced")
			}
			return
		}
	}
	c.events = append(c.events, event)
	c.sortLocked()
	if c.log != nil {
		c.log.WithField("event_id", event.ID).Debug("calendar: event added")
	}
}

func (c *Calendar) AddEvents(events []*Event) {
	for _, e := range events {
		c.AddEvent(e)
	}
}

func (c *Calendar) RemoveEvent(eventID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i, e := range c.events {
		if e.ID == eventID {
			c.events = append(c.events[:i], c.events[i+1:]...)
			if c.log != nil {
				c.log.WithField("event_id", eventID).Debug("calendar: event removed")
			}
			return true
		}
	}
	return false
}

func (c *Calendar) GetEvent(eventID string) *Event {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, e := range c.events {
		if e.ID == eventID {
			return e
		}
	}
	return nil
}

func (c *Calendar) GetUpcoming(now time.Time, n int, minSeverity Severity) []*Event {
	c.mu.RLock()
	defer c.mu.RUnlock()

	minRank := severityRank(minSeverity)
	result := make([]*Event, 0, n)
	for _, e := range c.events {
		if e.StartTime.Before(now) {
			continue
		}
		if severityRank(e.Severity) < minRank {
			continue
		}
		result = append(result, e)
		if len(result) >= n {
			break
		}
	}
	return result
}

func (c *Calendar) AssessImpact(now time.Time) *ImpactAssessment {
	c.mu.RLock()
	var residual *Event
	for _, e := range c.events {
		if !e.AffectsCrypto {
			continue
		}
		end := e.StartTime.Add(e.Duration)
		if !e.StartTime.After(now) && now.Before(end) {
			residual = e
			break
		}
	}
	c.mu.RUnlock()

	upcoming := c.filterUpcomingCrypto(now, 5, SeverityMedium)

	if residual == nil && len(upcoming) == 0 {
		return &ImpactAssessment{
			HasImpact:               false,
			SuggestedAction:         "NORMAL",
			SuggestedSizeMultiplier: 1.0,
			Reasoning:               "no upcoming high-impact events",
		}
	}

	if residual != nil {
		action := "REDUCE_SIZE_75"
		mult := 0.75
		if residual.Severity == SeverityCritical {
			action = "REDUCE_SIZE_50"
			mult = 0.5
		}
		return &ImpactAssessment{
			HasImpact:               true,
			NextEvent:               residual,
			Window:                  WindowPast,
			TimeUntilEvent:          0,
			SuggestedAction:         action,
			SuggestedSizeMultiplier: mult,
			Reasoning:               fmt.Sprintf("residual impact from %s (%s)", residual.Title, residual.Severity),
		}
	}

	next := upcoming[0]
	timeUntil := next.StartTime.Sub(now)

	var window ProximityWindow
	var action string
	var mult float64

	switch {
	case timeUntil < 30*time.Minute:
		window = WindowImminent
		switch next.Severity {
		case SeverityCritical:
			action, mult = "PAUSE", 0.0
		case SeverityHigh:
			action, mult = "REDUCE_SIZE_25", 0.25
		default:
			action, mult = "REDUCE_SIZE_50", 0.5
		}
	case timeUntil < 2*time.Hour:
		window = WindowNear
		switch next.Severity {
		case SeverityCritical:
			action, mult = "REDUCE_SIZE_50", 0.5
		case SeverityHigh:
			action, mult = "REDUCE_SIZE_75", 0.75
		default:
			action, mult = "NORMAL", 1.0
		}
	case timeUntil < 6*time.Hour:
		window = WindowSoon
		action, mult = "NORMAL", 1.0
	default:
		window = WindowFar
		action, mult = "NORMAL", 1.0
	}

	hasImpact := mult < 1.0
	return &ImpactAssessment{
		HasImpact:               hasImpact,
		NextEvent:               next,
		Window:                  window,
		TimeUntilEvent:          timeUntil,
		SuggestedAction:         action,
		SuggestedSizeMultiplier: mult,
		Reasoning:               fmt.Sprintf("%s in %s (severity=%s, window=%s)", next.Title, timeUntil.Round(time.Minute), next.Severity, window),
	}
}

// LoadDefaultCryptoEvents loads recurring known events. Currently this calls
// LoadDefaultEvents which seeds the calendar with FOMC, CPI and NFP dates
// for 2026.
func (c *Calendar) LoadDefaultCryptoEvents() {
	LoadDefaultEvents(c)
}

func (c *Calendar) PurgePast(now time.Time) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	kept := make([]*Event, 0, len(c.events))
	removed := 0
	for _, e := range c.events {
		if e.StartTime.Add(e.Duration).Before(now) {
			removed++
			continue
		}
		kept = append(kept, e)
	}
	c.events = kept
	return removed
}

func (c *Calendar) sortLocked() {
	sort.SliceStable(c.events, func(i, j int) bool {
		return c.events[i].StartTime.Before(c.events[j].StartTime)
	})
}

func (c *Calendar) filterUpcomingCrypto(now time.Time, n int, minSeverity Severity) []*Event {
	c.mu.RLock()
	defer c.mu.RUnlock()

	minRank := severityRank(minSeverity)
	result := make([]*Event, 0, n)
	for _, e := range c.events {
		if !e.AffectsCrypto {
			continue
		}
		if e.StartTime.Before(now) {
			continue
		}
		if severityRank(e.Severity) < minRank {
			continue
		}
		result = append(result, e)
		if len(result) >= n {
			break
		}
	}
	return result
}
