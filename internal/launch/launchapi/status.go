package launchapi

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rantipay/trading-mcp/internal/launch/core"
)

// parseSources accepts the sources column either as an array of source
// objects or as an object keyed by source name.
func parseSources(raw []byte) []SourceStatus {
	out := []SourceStatus{}
	if len(raw) == 0 {
		return out
	}
	if err := json.Unmarshal(raw, &out); err == nil {
		return out
	}
	var byName map[string]SourceStatus
	if err := json.Unmarshal(raw, &byName); err != nil {
		return []SourceStatus{}
	}
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)
	out = out[:0]
	for _, n := range names {
		s := byName[n]
		if s.Name == "" {
			s.Name = n
		}
		out = append(out, s)
	}
	return out
}

// GetStatus assembles the console summary. now is injectable for tests; zero
// means time.Now().
func GetStatus(ctx context.Context, db core.DB, req GateRequirements, now time.Time) (Status, error) {
	if now.IsZero() {
		now = time.Now()
	}
	st := Status{
		Service:      "never_started",
		Breaker:      Breaker{State: "running"},
		Sources:      []SourceStatus{},
		NetworkState: map[string]string{},
		Counts24h:    Counts24h{ByReason: map[string]int{}, ByBand: map[string]int{}},
	}

	// Heartbeat: the freshest instance.
	var instance string
	var hb time.Time
	var paused bool
	var pauseReason *string
	var netRaw, srcRaw []byte
	err := db.QueryRow(ctx,
		`SELECT instance, heartbeat_at, entries_paused, pause_reason, network_state, sources
		 FROM launch.service_state ORDER BY heartbeat_at DESC LIMIT 1`).
		Scan(&instance, &hb, &paused, &pauseReason, &netRaw, &srcRaw)
	switch {
	case err == nil:
		hbu := hb.UTC()
		st.LastHeartbeat = &hbu
		st.Instance = &instance
		st.Service = "up"
		if now.Sub(hb) > HeartbeatStaleAfter {
			st.Service = "down"
		}
		st.EntriesPaused = paused
		st.PauseReason = pauseReason
		st.Sources = parseSources(srcRaw)
		if len(netRaw) > 0 {
			var m map[string]string
			if json.Unmarshal(netRaw, &m) == nil && m != nil {
				st.NetworkState = m
			}
		}
	case errors.Is(err, pgx.ErrNoRows):
	default:
		return st, err
	}

	// Breaker: latest global event.
	var toState, actor, reason string
	var since time.Time
	err = db.QueryRow(ctx,
		`SELECT to_state, actor, reason, ts FROM launch.circuit_breaker_events
		 WHERE scope = 'global' ORDER BY id DESC LIMIT 1`).Scan(&toState, &actor, &reason, &since)
	switch {
	case err == nil:
		su := since.UTC()
		st.Breaker = Breaker{State: "running", Since: &su, Actor: actor, Reason: reason}
		if strings.HasPrefix(strings.ToLower(toState), "kill") {
			st.Breaker.State = "killed"
			st.EntriesPaused = true
			kr := "KILL_SWITCH"
			st.PauseReason = &kr
		}
	case errors.Is(err, pgx.ErrNoRows):
	default:
		return st, err
	}

	gate, err := GateLatest(ctx, db, req)
	if err != nil {
		return st, err
	}
	st.Gate = gate

	// Shadow aggregates: counterfactual positions never count as PnL/exposure.
	err = db.QueryRow(ctx, `
SELECT (count(*) FILTER (WHERE state = 'OPEN'))::int,
       COALESCE(sum(invested_usd) FILTER (WHERE state = 'OPEN'), 0)::float8,
       (count(*) FILTER (WHERE state = 'CLOSED'))::int,
       COALESCE(sum(net_pnl_usd) FILTER (WHERE state = 'CLOSED'), 0)::float8,
       COALESCE(sum(gross_pnl_usd) FILTER (WHERE state = 'CLOSED'), 0)::float8
FROM launch.positions WHERE mode = 'shadow' AND entry_channel <> 'counterfactual'`).
		Scan(&st.Shadow.OpenPositions, &st.Shadow.ExposureUSD, &st.Shadow.ClosedPositions, &st.Shadow.NetPnLUSD, &st.Shadow.GrossPnLUSD)
	if err != nil {
		return st, err
	}

	since24 := now.Add(-24 * time.Hour)
	if err := db.QueryRow(ctx, `SELECT count(*)::int FROM launch.raw_events WHERE ts > $1 AND NOT duplicate`, since24).
		Scan(&st.Counts24h.Events); err != nil {
		return st, err
	}
	if err := db.QueryRow(ctx,
		`SELECT count(*)::int, (count(*) FILTER (WHERE screen_result = 'REJECTED'))::int
		 FROM launch.opportunities WHERE created_at > $1`, since24).
		Scan(&st.Counts24h.Opportunities, &st.Counts24h.Rejected); err != nil {
		return st, err
	}
	if err := groupCount(ctx, db,
		`SELECT reject_reason, count(*)::int FROM launch.opportunities
		 WHERE created_at > $1 AND reject_reason IS NOT NULL GROUP BY reject_reason`, since24, st.Counts24h.ByReason); err != nil {
		return st, err
	}
	if err := groupCount(ctx, db,
		`SELECT band, count(*)::int FROM launch.opportunities
		 WHERE created_at > $1 AND band IS NOT NULL GROUP BY band`, since24, st.Counts24h.ByBand); err != nil {
		return st, err
	}
	return st, nil
}

func groupCount(ctx context.Context, db core.DB, q string, since time.Time, dst map[string]int) error {
	rows, err := db.Query(ctx, q, since)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return err
		}
		dst[k] = n
	}
	return rows.Err()
}
