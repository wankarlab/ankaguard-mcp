package calendar

import "time"

// LoadDefaultEvents popula un Calendar con eventos macro recurrentes conocidos.
// Esta función es idempotente: pasar el mismo Calendar varias veces no
// duplica eventos (gracias al ID-based replace en AddEvent).
func LoadDefaultEvents(c *Calendar) {
	if c == nil {
		return
	}

	events := []*Event{
		// FOMC 2026 (approximate — adjust when official calendar is published)
		// FOMC meetings typically on the third Wednesday of certain months
		newFOMCEvent("fomc-2026-01", time.Date(2026, 1, 28, 19, 0, 0, 0, time.UTC)),
		newFOMCEvent("fomc-2026-03", time.Date(2026, 3, 18, 18, 0, 0, 0, time.UTC)),
		newFOMCEvent("fomc-2026-05", time.Date(2026, 5, 6, 18, 0, 0, 0, time.UTC)),
		newFOMCEvent("fomc-2026-06", time.Date(2026, 6, 17, 18, 0, 0, 0, time.UTC)),
		newFOMCEvent("fomc-2026-07", time.Date(2026, 7, 29, 18, 0, 0, 0, time.UTC)),
		newFOMCEvent("fomc-2026-09", time.Date(2026, 9, 16, 18, 0, 0, 0, time.UTC)),
		newFOMCEvent("fomc-2026-11", time.Date(2026, 11, 4, 19, 0, 0, 0, time.UTC)),
		newFOMCEvent("fomc-2026-12", time.Date(2026, 12, 16, 19, 0, 0, 0, time.UTC)),

		// CPI release (approximate — typically mid-month)
		newCPIEvent("cpi-2026-01", time.Date(2026, 1, 14, 13, 30, 0, 0, time.UTC)),
		newCPIEvent("cpi-2026-02", time.Date(2026, 2, 11, 13, 30, 0, 0, time.UTC)),
		newCPIEvent("cpi-2026-03", time.Date(2026, 3, 11, 12, 30, 0, 0, time.UTC)),
		newCPIEvent("cpi-2026-04", time.Date(2026, 4, 14, 12, 30, 0, 0, time.UTC)),
		newCPIEvent("cpi-2026-05", time.Date(2026, 5, 13, 12, 30, 0, 0, time.UTC)),
		newCPIEvent("cpi-2026-06", time.Date(2026, 6, 10, 12, 30, 0, 0, time.UTC)),
		newCPIEvent("cpi-2026-07", time.Date(2026, 7, 15, 12, 30, 0, 0, time.UTC)),
		newCPIEvent("cpi-2026-08", time.Date(2026, 8, 12, 12, 30, 0, 0, time.UTC)),
		newCPIEvent("cpi-2026-09", time.Date(2026, 9, 11, 12, 30, 0, 0, time.UTC)),
		newCPIEvent("cpi-2026-10", time.Date(2026, 10, 13, 12, 30, 0, 0, time.UTC)),
		newCPIEvent("cpi-2026-11", time.Date(2026, 11, 12, 13, 30, 0, 0, time.UTC)),
		newCPIEvent("cpi-2026-12", time.Date(2026, 12, 10, 13, 30, 0, 0, time.UTC)),

		// NFP — Non-Farm Payrolls — first Friday of each month
		newNFPEvent("nfp-2026-01", time.Date(2026, 1, 2, 13, 30, 0, 0, time.UTC)),
		newNFPEvent("nfp-2026-02", time.Date(2026, 2, 6, 13, 30, 0, 0, time.UTC)),
		newNFPEvent("nfp-2026-03", time.Date(2026, 3, 6, 13, 30, 0, 0, time.UTC)),
		newNFPEvent("nfp-2026-04", time.Date(2026, 4, 3, 12, 30, 0, 0, time.UTC)),
		newNFPEvent("nfp-2026-05", time.Date(2026, 5, 1, 12, 30, 0, 0, time.UTC)),
		newNFPEvent("nfp-2026-06", time.Date(2026, 6, 5, 12, 30, 0, 0, time.UTC)),
		newNFPEvent("nfp-2026-07", time.Date(2026, 7, 3, 12, 30, 0, 0, time.UTC)),
		newNFPEvent("nfp-2026-08", time.Date(2026, 8, 7, 12, 30, 0, 0, time.UTC)),
		newNFPEvent("nfp-2026-09", time.Date(2026, 9, 4, 12, 30, 0, 0, time.UTC)),
		newNFPEvent("nfp-2026-10", time.Date(2026, 10, 2, 12, 30, 0, 0, time.UTC)),
		newNFPEvent("nfp-2026-11", time.Date(2026, 11, 6, 13, 30, 0, 0, time.UTC)),
		newNFPEvent("nfp-2026-12", time.Date(2026, 12, 4, 13, 30, 0, 0, time.UTC)),
	}

	c.AddEvents(events)
}

func newFOMCEvent(id string, t time.Time) *Event {
	return &Event{
		ID:            id,
		Title:         "FOMC Rate Decision",
		Description:   "Federal Reserve interest rate decision and statement",
		StartTime:     t,
		Duration:      4 * time.Hour,
		Severity:      SeverityCritical,
		Category:      CategoryMonetary,
		Country:       "US",
		AffectsCrypto: true,
	}
}

func newCPIEvent(id string, t time.Time) *Event {
	return &Event{
		ID:            id,
		Title:         "US CPI Release",
		Description:   "Consumer Price Index inflation data",
		StartTime:     t,
		Duration:      2 * time.Hour,
		Severity:      SeverityCritical,
		Category:      CategoryInflation,
		Country:       "US",
		AffectsCrypto: true,
	}
}

func newNFPEvent(id string, t time.Time) *Event {
	return &Event{
		ID:            id,
		Title:         "US Non-Farm Payrolls",
		Description:   "Monthly employment data release",
		StartTime:     t,
		Duration:      2 * time.Hour,
		Severity:      SeverityHigh,
		Category:      CategoryEmployment,
		Country:       "US",
		AffectsCrypto: true,
	}
}
