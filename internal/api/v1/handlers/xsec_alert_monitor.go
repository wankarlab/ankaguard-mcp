package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/xsec"
)

// XSecAlertMonitor periodically evaluates the health of the live XSEC basket and
// pushes an alert (via RantiPay notificationpush + optional in-app WebSocket)
// when a signal crosses its threshold. It reuses the exact readers the /xsec
// endpoints use (open legs from xsec_live_book + enrichWithLivePnL), so the
// alert view can never drift from the dashboard view.
//
// Dedup is persistent (xsec_alert_state table): a sustained signal fires once on
// the ok→firing transition and clears on firing→ok; job-failure fires once per
// new errored job id. The table is the source of truth, so state survives
// restarts with no in-memory load step.
type XSecAlertMonitor struct {
	h        *XSecHandler
	db       *sql.DB
	notifier alertNotifier
	userID   string
	log      *logrus.Logger
	interval time.Duration
	stopCh   chan struct{}
}

// alertNotifier is the push side (satisfied by *notifications.RantiPayPushClient);
// an interface so tests can inject a fake.
//
// In-app visibility is NOT delivered here: it comes from the polled
// GET /xsec/status payload (active_alerts read from xsec_alert_state), matching
// how the rest of the dashboard receives data (SWR polling, not the auth-gated
// WebSocket hub).
type alertNotifier interface {
	SendToUser(ctx context.Context, userID, title, body string, data map[string]interface{}) error
}

// Threshold param keys (xsec_params, hot-reloaded each tick).
const (
	keyAlertEnabled      = "xsec.alert.enabled"
	keyAlertImbalancePct = "xsec.alert.imbalance_pct"
	keyAlertRebalanceDay = "xsec.alert.rebalance_days"
	keyAlertLegPnLPct    = "xsec.alert.leg_pnl_pct"
	keyAlertIntervalMin  = "xsec.alert.interval_min"
	keyAlertOnImbalance  = "xsec.alert.on_imbalance"
	keyAlertOnStale      = "xsec.alert.on_staleness"
	keyAlertOnLeg        = "xsec.alert.on_leg_blowout"
	keyAlertOnJobFail    = "xsec.alert.on_job_failure"
)

// AlertParamKeys are the alert-threshold keys the ParamsEditor may write (added
// to the PUT /xsec/params whitelist).
var AlertParamKeys = []string{
	keyAlertEnabled, keyAlertImbalancePct, keyAlertRebalanceDay, keyAlertLegPnLPct,
	keyAlertIntervalMin, keyAlertOnImbalance, keyAlertOnStale, keyAlertOnLeg, keyAlertOnJobFail,
}

type alertThresholds struct {
	enabled       bool
	imbalancePct  float64
	rebalanceDays float64
	legPnLPct     float64
	onImbalance   bool
	onStale       bool
	onLeg         bool
	onJobFail     bool
}

// NewXSecAlertMonitor builds a monitor. interval comes from
// xsec.alert.interval_min (default 5m) read once here; other thresholds
// hot-reload each tick. broadcaster may be nil (push-only).
func NewXSecAlertMonitor(h *XSecHandler, db *sql.DB, notifier alertNotifier, userID string, log *logrus.Logger) *XSecAlertMonitor {
	if log == nil {
		log = logrus.New()
	}
	interval := 5 * time.Minute
	if params, err := xsec.LoadParams(db); err == nil {
		if v, ok := params[keyAlertIntervalMin]; ok {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				interval = time.Duration(n) * time.Minute
			}
		}
	}
	return &XSecAlertMonitor{
		h: h, db: db, notifier: notifier,
		userID: userID, log: log, interval: interval, stopCh: make(chan struct{}),
	}
}

// Start runs one evaluation immediately, then on the configured interval.
func (m *XSecAlertMonitor) Start(ctx context.Context) {
	if err := ensureAlertStateSchema(m.db); err != nil {
		m.log.WithError(err).Warn("xsec alerts: could not ensure state table; monitor not started")
		return
	}
	m.tick(ctx)
	go func() {
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.tick(ctx)
			case <-m.stopCh:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	m.log.WithField("interval", m.interval.String()).Info("XSEC alert monitor started")
}

// Stop halts the monitor.
func (m *XSecAlertMonitor) Stop() {
	select {
	case m.stopCh <- struct{}{}:
	default:
	}
}

func (m *XSecAlertMonitor) tick(ctx context.Context) {
	thr := m.loadThresholds()
	if !thr.enabled {
		return
	}
	tickCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Sustained signals (imbalance / staleness / leg blow-out) share one basket read.
	for _, sig := range m.evaluateSustained(tickCtx, thr) {
		m.processSustained(tickCtx, sig)
	}
	// Event signal (job failure) is edge-triggered on a new errored job id.
	if thr.onJobFail {
		m.processJobFailure(tickCtx)
	}
}

func (m *XSecAlertMonitor) loadThresholds() alertThresholds {
	t := alertThresholds{
		enabled: true, imbalancePct: 15, rebalanceDays: 10, legPnLPct: -15,
		onImbalance: true, onStale: true, onLeg: true, onJobFail: true,
	}
	params, err := xsec.LoadParams(m.db)
	if err != nil {
		return t
	}
	t.enabled = paramBool(params, keyAlertEnabled, t.enabled)
	t.imbalancePct = paramFloat(params, keyAlertImbalancePct, t.imbalancePct)
	t.rebalanceDays = paramFloat(params, keyAlertRebalanceDay, t.rebalanceDays)
	t.legPnLPct = paramFloat(params, keyAlertLegPnLPct, t.legPnLPct)
	t.onImbalance = paramBool(params, keyAlertOnImbalance, t.onImbalance)
	t.onStale = paramBool(params, keyAlertOnStale, t.onStale)
	t.onLeg = paramBool(params, keyAlertOnLeg, t.onLeg)
	t.onJobFail = paramBool(params, keyAlertOnJobFail, t.onJobFail)
	return t
}

// alertSignal is one sustained health check for a single tick.
type alertSignal struct {
	key    string // stable dedup key
	firing bool
	title  string
	body   string
}

// evaluateSustained reads the basket once and returns the enabled sustained
// signals with their current firing state.
func (m *XSecAlertMonitor) evaluateSustained(ctx context.Context, thr alertThresholds) []alertSignal {
	var out []alertSignal

	positions := m.readOpenLegs(ctx)
	if len(positions) > 0 {
		positions = m.h.enrichWithLivePnL(ctx, positions)
	}

	// Imbalance
	if thr.onImbalance {
		var net, gross float64
		for _, p := range positions {
			net += float64(p.Dir) * p.Notional
			gross += absF(p.Notional)
		}
		imb := 0.0
		if gross > 0 {
			imb = absF(net) / gross * 100
		}
		firing := gross > 0 && imb > thr.imbalancePct
		out = append(out, alertSignal{
			key:    "imbalance",
			firing: firing,
			title:  "⚠ Cesta desbalanceada",
			body:   fmt.Sprintf("Imbalance %.1f%% (umbral %.0f%%) — beta direccional. Revisá /xsec.", imb, thr.imbalancePct),
		})
	}

	// Leg blow-out (worst adverse leg)
	if thr.onLeg {
		worst := 0.0
		worstPair := ""
		for _, p := range positions {
			if p.UnrealizedPnL == nil || p.Notional <= 0 {
				continue
			}
			pct := *p.UnrealizedPnL / p.Notional * 100
			if pct < worst {
				worst, worstPair = pct, p.Pair
			}
		}
		firing := worstPair != "" && worst <= thr.legPnLPct
		out = append(out, alertSignal{
			key:    "leg_blowout",
			firing: firing,
			title:  "🔴 Pata reventada",
			body:   fmt.Sprintf("%s en %.1f%% (umbral %.0f%%) — arrastra el libro. Revisá /xsec.", worstPair, worst, thr.legPnLPct),
		})
	}

	// Staleness (days since last rebalance-live)
	if thr.onStale {
		if last, ok := m.lastRebalanceLiveAt(ctx); ok {
			days := time.Since(last).Hours() / 24
			firing := days > thr.rebalanceDays
			out = append(out, alertSignal{
				key:    "staleness",
				firing: firing,
				title:  "⏱ Rebalance atrasado",
				body:   fmt.Sprintf("%.0f días desde el último rebalance (cadencia %.0fd). El momentum decae.", days, thr.rebalanceDays),
			})
		}
	}

	return out
}

// readOpenLegs mirrors GetStatus's open-leg query (pair/dir/opened_at/entry/notional).
func (m *XSecAlertMonitor) readOpenLegs(ctx context.Context) []XSecPosition {
	var positions []XSecPosition
	rows, err := m.db.QueryContext(ctx,
		`SELECT pair, dir, opened_at, entry_price, notional_usd FROM xsec_live_book WHERE status = 'open' ORDER BY dir DESC, pair`)
	if err != nil {
		m.log.WithError(err).Debug("xsec alerts: read legs failed")
		return positions
	}
	defer rows.Close()
	for rows.Next() {
		var p XSecPosition
		if rows.Scan(&p.Pair, &p.Dir, &p.OpenedAt, &p.Entry, &p.Notional) == nil {
			positions = append(positions, p)
		}
	}
	return positions
}

func (m *XSecAlertMonitor) lastRebalanceLiveAt(ctx context.Context) (time.Time, bool) {
	var ts sql.NullTime
	err := m.db.QueryRowContext(ctx,
		`SELECT finished_at FROM xsec_jobs WHERE mode='rebalance-live' AND status='done' AND finished_at IS NOT NULL ORDER BY id DESC LIMIT 1`).Scan(&ts)
	if err != nil || !ts.Valid {
		return time.Time{}, false
	}
	return ts.Time, true
}

// processSustained applies ok→firing (send) / firing→ok (recovered) transitions
// against the persisted state, so an alert is sent once per crossing.
func (m *XSecAlertMonitor) processSustained(ctx context.Context, sig alertSignal) {
	prevActive := m.stateActive(sig.key)
	switch {
	case sig.firing && !prevActive:
		m.dispatch(ctx, sig.key, sig.title, sig.body)
		m.setState(sig.key, true, 0, sig.title, sig.body)
	case !sig.firing && prevActive:
		m.dispatch(ctx, sig.key, "✅ Cesta recuperada", "La condición de alerta ("+sig.key+") volvió a la normalidad.")
		m.setState(sig.key, false, 0, "", "")
	}
}

// processJobFailure fires once per new errored rebalance job (edge-triggered).
func (m *XSecAlertMonitor) processJobFailure(ctx context.Context) {
	var maxID sql.NullInt64
	if err := m.db.QueryRowContext(ctx,
		`SELECT MAX(id) FROM xsec_jobs WHERE mode LIKE 'rebalance%' AND status='error'`).Scan(&maxID); err != nil || !maxID.Valid {
		return
	}
	// First run (no state row): baseline to the current max errored id WITHOUT
	// alerting, so a fresh deploy never fires for the pre-existing backlog —
	// only failures that happen after the monitor first sees the table alert.
	if !m.stateExists("job_failure") {
		m.setState("job_failure", false, float64(maxID.Int64), "", "")
		return
	}
	_, lastSeen := m.stateValue("job_failure")
	if float64(maxID.Int64) <= lastSeen {
		return
	}
	title := "❌ Rebalance falló"
	body := fmt.Sprintf("El job #%d (rebalance) terminó en error — posible partial-fill. Revisá el libro en /xsec.", maxID.Int64)
	m.dispatch(ctx, "job_failure", title, body)
	// Edge event, not a sustained state: record the seen id for dedup but keep
	// active=false so it does NOT linger in the polled active_alerts bar (the
	// resulting bad book state is captured by imbalance/leg-blowout instead).
	m.setState("job_failure", false, float64(maxID.Int64), "", "")
}

// dispatch sends the push (best-effort; delivery failure only logs). The
// persisted state (xsec_alert_state) is what the polled /xsec/status surfaces
// in-app, so a push failure never hides the alert on the dashboard.
func (m *XSecAlertMonitor) dispatch(ctx context.Context, key, title, body string) {
	data := map[string]interface{}{"kind": "xsec_alert", "alert": key, "screen": "xsec"}
	if m.notifier != nil {
		if err := m.notifier.SendToUser(ctx, m.userID, title, body, data); err != nil {
			m.log.WithError(err).WithField("alert", key).Debug("xsec alerts: push delivery error")
		}
	}
	m.log.WithFields(logrus.Fields{"alert": key, "title": title}).Info("xsec alert fired")
}

// ── persistent dedup state ────────────────────────────────────────────────────

func ensureAlertStateSchema(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("nil db")
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS xsec_alert_state (
		alert_key  TEXT PRIMARY KEY,
		active     INTEGER NOT NULL DEFAULT 0,
		last_value REAL,
		title      TEXT DEFAULT '',
		body       TEXT DEFAULT '',
		fired_at   TIMESTAMP,
		cleared_at TIMESTAMP
	)`)
	if err != nil {
		return fmt.Errorf("create xsec_alert_state: %w", err)
	}
	return nil
}

func (m *XSecAlertMonitor) stateActive(key string) bool {
	active, _ := m.stateValue(key)
	return active
}

// stateExists reports whether a state row for the key exists (used to
// distinguish a first-run baseline from a genuine transition).
func (m *XSecAlertMonitor) stateExists(key string) bool {
	var n int
	if err := m.db.QueryRow(`SELECT COUNT(*) FROM xsec_alert_state WHERE alert_key = ?`, key).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// stateValue returns (active, last_value) for a key; (false, 0) when absent.
func (m *XSecAlertMonitor) stateValue(key string) (bool, float64) {
	var active int
	var lastValue sql.NullFloat64
	err := m.db.QueryRow(`SELECT active, last_value FROM xsec_alert_state WHERE alert_key = ?`, key).Scan(&active, &lastValue)
	if err != nil {
		return false, 0
	}
	return active == 1, lastValue.Float64
}

func (m *XSecAlertMonitor) setState(key string, active bool, lastValue float64, title, body string) {
	a := 0
	col := "cleared_at"
	if active {
		a = 1
		col = "fired_at"
	}
	q := fmt.Sprintf(`INSERT INTO xsec_alert_state (alert_key, active, last_value, title, body, %s)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(alert_key) DO UPDATE SET active = excluded.active, last_value = excluded.last_value,
			title = excluded.title, body = excluded.body, %s = CURRENT_TIMESTAMP`, col, col)
	if _, err := m.db.Exec(q, key, a, lastValue, title, body); err != nil {
		m.log.WithError(err).WithField("alert", key).Debug("xsec alerts: state upsert failed")
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func absF(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func paramFloat(p map[string]string, key string, def float64) float64 {
	if v, ok := p[key]; ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func paramBool(p map[string]string, key string, def bool) bool {
	if v, ok := p[key]; ok {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}
