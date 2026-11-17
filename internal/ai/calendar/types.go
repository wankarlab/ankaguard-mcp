package calendar

import "time"

type Severity string

const (
	SeverityCritical Severity = "CRITICAL"
	SeverityHigh     Severity = "HIGH"
	SeverityMedium   Severity = "MEDIUM"
	SeverityLow      Severity = "LOW"
)

type EventCategory string

const (
	CategoryMonetary   EventCategory = "MONETARY"
	CategoryInflation  EventCategory = "INFLATION"
	CategoryEmployment EventCategory = "EMPLOYMENT"
	CategoryGrowth     EventCategory = "GROWTH"
	CategoryCrypto     EventCategory = "CRYPTO"
	CategoryOther      EventCategory = "OTHER"
)

type Event struct {
	ID            string
	Title         string
	Description   string
	StartTime     time.Time
	Duration      time.Duration
	Severity      Severity
	Category      EventCategory
	Country       string
	AffectsCrypto bool
}

type ProximityWindow string

const (
	WindowImminent ProximityWindow = "IMMINENT"
	WindowNear     ProximityWindow = "NEAR"
	WindowSoon     ProximityWindow = "SOON"
	WindowFar      ProximityWindow = "FAR"
	WindowPast     ProximityWindow = "PAST"
)

type ImpactAssessment struct {
	HasImpact               bool
	NextEvent               *Event
	Window                  ProximityWindow
	TimeUntilEvent          time.Duration
	SuggestedAction         string
	SuggestedSizeMultiplier float64
	Reasoning               string
}
