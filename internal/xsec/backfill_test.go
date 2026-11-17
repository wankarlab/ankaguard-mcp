package xsec

import (
	"strconv"
	"testing"
	"time"
)

func TestParseDailyKlines(t *testing.T) {
	// now = 2026-07-23 14:00 UTC → today's forming bar (2026-07-23 00:00) must be dropped.
	now := time.Date(2026, 7, 23, 14, 0, 0, 0, time.UTC)
	day := func(y, m, d int) string {
		return strconv.FormatInt(time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC).UnixMilli(), 10)
	}

	payload := []byte(`{"code":"200000","data":[
		[` + day(2026, 7, 21) + `,1.0,1.2,0.9,1.1,500,550.5],
		[` + day(2026, 7, 22) + `,1.1,1.3,1.0,1.2,600,720.0],
		[` + day(2026, 7, 23) + `,1.2,1.4,1.1,1.3,100,130.0]
	]}`)

	candles, err := parseDailyKlines(payload, now)
	if err != nil {
		t.Fatalf("parseDailyKlines: %v", err)
	}
	if len(candles) != 2 {
		t.Fatalf("want 2 closed candles (today's forming bar dropped), got %d", len(candles))
	}
	last := candles[len(candles)-1]
	if got := last.Timestamp.UTC(); !got.Equal(time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("last closed bar timestamp: want 2026-07-22, got %v", got)
	}
	if last.Close != 1.2 {
		t.Errorf("last close: want 1.2, got %v", last.Close)
	}
	if last.Volume != 600 {
		t.Errorf("last volume: want 600, got %v", last.Volume)
	}
}

func TestParseDailyKlinesSkipsShortRows(t *testing.T) {
	now := time.Date(2026, 7, 23, 14, 0, 0, 0, time.UTC)
	ts := strconv.FormatInt(time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC).UnixMilli(), 10)
	payload := []byte(`{"code":"200000","data":[[` + ts + `,1.0,1.2],[` + ts + `,1.0,1.2,0.9,1.1,500]]}`)
	candles, err := parseDailyKlines(payload, now)
	if err != nil {
		t.Fatalf("parseDailyKlines: %v", err)
	}
	if len(candles) != 1 {
		t.Fatalf("want 1 candle (short row skipped), got %d", len(candles))
	}
}

func TestParseDailyKlinesRejectsGarbage(t *testing.T) {
	if _, err := parseDailyKlines([]byte(`not json`), time.Now()); err == nil {
		t.Fatal("want error on malformed payload, got nil")
	}
}
