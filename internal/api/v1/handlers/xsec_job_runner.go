// Package handlers — async job runner for cmd/xsec-live (F4.7).
//
// xsec-live manages a REAL-money market-neutral futures basket on KuCoin. A
// rebalance can take up to ~15 minutes (maker-first waits up to -maker-timeout
// per leg), so the REST API never runs it synchronously: it spawns the
// xsec-live binary as a subprocess, records progress in the xsec_jobs table
// (internal/xsec/jobs.go), and lets the caller poll GET /xsec/jobs/:id.
//
// Safety rules enforced HERE, not trusted from the client:
//   - argv is built server-side from a small whitelist of numeric overrides —
//     the client never supplies raw flags.
//   - -live and -i-verified-futures-sizing are appended ONLY for mode
//     rebalance-live, and appended exactly once.
//   - rebalance-live requires a completed rebalance-dry job with IDENTICAL
//     overrides in the last dryRunFirstWindow (dry-run-first).
//   - at most one job runs at a time (single-flight).
package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/killswitch"
	"github.com/rantipay/trading-mcp/internal/xsec"
)

// Job modes accepted by POST /api/v1/xsec/jobs.
const (
	XSecJobModeSyncBook      = "sync-book"
	XSecJobModeRebalanceDry  = "rebalance-dry"
	XSecJobModeRebalanceLive = "rebalance-live"
	XSecJobModeCancelOrder   = "cancel-order"
	XSecJobModeCloseLeg      = "close-leg"
	// XSecJobModeBacktest runs cmd/xsec-sim (a different binary — see
	// binPathForMode) over local candle_archive history. Read-only: no
	// exchange calls, no money at risk, so it needs neither Level 2 (see
	// xsecJobModesRequiringLevel2 in xsec_jobs_handler.go) nor dry-run-first.
	XSecJobModeBacktest = "backtest"
	// XSecJobModeBacktestSweep runs cmd/xsec-sim once per value in
	// XSecJobRequest.SweepValues, varying the single dimension named by
	// SweepParam ("lb"|"k"|"rebal"|"band") while holding the rest of the
	// config fixed — a 1-D sensitivity sweep. Same read-only/no-Level-2
	// posture as XSecJobModeBacktest; see execute()'s sweep branch.
	XSecJobModeBacktestSweep = "backtest-sweep"
	// XSecJobModeShadowRecord runs cmd/xsec-bot -shadow-only (a THIRD binary —
	// see binPathForMode): it records the candidate/live/LTR shadow baskets for
	// the current rebalance date WITHOUT touching the paper portfolio or the
	// exchange. Read-only w.r.t. money; used to seed a t0 anchor and to force a
	// forward paper A/B data point on demand instead of waiting for the weekly
	// cron. Not Level-2 (no money), not dry-run-first.
	XSecJobModeShadowRecord = "shadow-record"
	// XSecJobModeCloseAll liquidates every open leg (cmd/xsec-live -close-all):
	// de-risking, opens nothing, but mutates real positions → Level 2.
	XSecJobModeCloseAll = "close-all"
	// XSecJobModeRelaunch liquidates everything AND re-deploys a fresh basket in
	// one run (cmd/xsec-live -close-all -rebalance -live). Opens real positions →
	// Level 2 + UI double-confirm.
	XSecJobModeRelaunch = "relaunch"
	// XSecJobModeNeutralize trims the heavy side back to dollar-neutral
	// (cmd/xsec-live -neutralize): reduce-only de-risking, opens nothing, but
	// mutates real positions → Level 2. The one-click apply for the hybrid
	// imbalance advisor.
	XSecJobModeNeutralize = "neutralize"
	// XSecJobModeAutoconfig runs cmd/xsec-live -autoconfig: read-only sizing
	// search (largest k whose per-side selection fills completely at the real
	// account equity) that places NO orders and mutates nothing. Same posture
	// as rebalance-dry: no Level 2, no dry-run-first. Its last stdout line is
	// "AUTOCONFIG_RESULT {json}" (see cmd/xsec-live/autoconfig.go).
	XSecJobModeAutoconfig = "autoconfig"
)

// Live-sizing bounds for the rebalance/relaunch modes. Widened from the
// original micro-book caps (50 / 500) so the assisted parametrization for a
// real account (e.g. $4000) is actually deployable — see
// GET /api/v1/xsec/suggest-params. Still hard-bounded so a fat-finger can't
// push $1M/leg by hand.
const (
	xsecMaxPosUSDMin   = 0.5
	xsecMaxPosUSDMax   = 5000.0
	xsecMaxTotalUSDMin = 5.0
	xsecMaxTotalUSDMax = 100000.0
)

// dryRunFirstWindow is how recent a matching rebalance-dry job must be for a
// rebalance-live request to be allowed to proceed.
const dryRunFirstWindow = 10 * time.Minute

// xsecJobTimeout is the hard wall-clock cap on the xsec-live subprocess. A
// rebalance can legitimately take ~15 minutes (maker-first waits per leg);
// 30 minutes leaves headroom without letting a hung subprocess run forever.
const xsecJobTimeout = 30 * time.Minute

// xsecJobFlushInterval is how often the runner flushes captured stdout+stderr
// to the DB row while the subprocess is still running.
const xsecJobFlushInterval = 2 * time.Second

var cancelOrderIDPattern = regexp.MustCompile(`^[0-9]{10,25}$`)

// pairPattern matches an xsec basket pair as cmd/xsec-live's ranker emits it
// (e.g. "AAVE-USDT") — uppercase base, dash, USDT quote. Same defensive
// whitelist spirit as cancelOrderIDPattern: reject anything that isn't
// obviously a pair before it ever reaches a shell argv.
var pairPattern = regexp.MustCompile(`^[A-Z0-9]{1,15}-USDT$`)

// XSecJobRequest is the validated, server-normalized shape of a job creation
// request. K/MaxPosUSD/Lookback/MaxTotalUSD are optional overrides for the
// rebalance modes; K/Lookback are reused as-is by the backtest mode (same
// meaning: basket size per side / momentum lookback). Rebal..Windows are
// backtest-only.
type XSecJobRequest struct {
	Mode        string
	K           *int
	MaxPosUSD   *float64
	Lookback    *int
	MaxTotalUSD *float64
	OrderID     string
	Pair        string

	// Autoconfig-only: fraction of equity to deploy (0,1]. Capital (below) is
	// reused as the optional equity override (0/absent = read the real one).
	Utilization *float64

	// Backtest-only overrides (cmd/xsec-sim flags).
	Rebal          *int
	Band           *int
	Cost           *float64
	Funding        *float64
	Capital        *float64
	VolWeights     *bool
	MinTurnoverUSD *float64
	Windows        *int

	// Backtest-only, advanced (Fase 3) — execution realism, intra-period risk
	// management, and OOS hold-out. All optional; omitted = xsec-sim's own
	// flag default (legacy/off) applies.
	StopShort      *float64
	StopLong       *float64
	TPLiquidate    *float64
	TPLeg          *float64
	TPRebalance    *float64
	TrailTP        *float64
	RegimeFloor    *float64
	RegimeWindow   *int
	MakerFillProb  *float64
	TakerCost      *float64
	VolTarget      *float64
	MaxShortWeight *float64
	FundingTilt    *float64
	Pairs          string
	StartFrac      *float64
	EndFrac        *float64

	// Backtest-sweep-only: SweepParam names which of lb/k/rebal/band the
	// sweep varies (that field, if also set above, is ignored — see
	// buildBacktestArgs' skip parameter); SweepValues is the list of values
	// to run it at, one xsec-sim invocation each (see sweepParamFlag).
	SweepParam  string
	SweepValues []float64
}

// XSecJobStartError carries an HTTP status alongside a message so the handler
// can translate it without re-deriving the reason.
type XSecJobStartError struct {
	Status       int
	Message      string
	RunningJobID int64 // populated only for single-flight (409) conflicts
	Code         string // optional API error code overriding XSEC_JOB_REJECTED (e.g. KILLSWITCH_ACTIVE)
}

func (e *XSecJobStartError) Error() string { return e.Message }

// xsecModeIncreasesRisk reports whether a job mode can open or enlarge
// positions. Ring 0 (global kill switch) refuses exactly these. De-risking
// modes (close-all, close-leg, neutralize, cancel-order, sync-book) and
// read-only ones (rebalance-dry, backtest, autoconfig, shadow-record) stay
// allowed so the operator can still unwind while ring 0 is active.
func xsecModeIncreasesRisk(mode string) bool {
	switch mode {
	case XSecJobModeRebalanceLive, XSecJobModeRelaunch:
		return true
	}
	return false
}

// XSecJobRunner owns the mode->argv mapping, the single-flight guard, and
// subprocess execution for cmd/xsec-live (and, for XSecJobModeBacktest,
// cmd/xsec-sim) jobs.
type XSecJobRunner struct {
	db         *sql.DB
	log        *logrus.Logger
	binPath    string // cmd/xsec-live — every mode except backtest/shadow-record
	simBinPath string // cmd/xsec-sim — backtest only
	botBinPath string // cmd/xsec-bot — shadow-record only
	dbPath     string
	timeout    time.Duration

	mu sync.Mutex // guards the running-check + job-row-creation critical section
}

// NewXSecJobRunner creates a runner over the shared trading DB. binPath
// defaults to $XSEC_LIVE_BIN or "bin/xsec-live" (relative to the server's
// CWD, which under systemd is the repo root — see cmd/xsec-live/main.go and
// the deployment unit). simBinPath defaults to $XSEC_SIM_BIN or
// "bin/xsec-sim", same relative-path convention. dbPath is the -db flag
// passed to every invocation.
func NewXSecJobRunner(db *sql.DB, log *logrus.Logger) *XSecJobRunner {
	if log == nil {
		log = logrus.New()
	}
	binPath := os.Getenv("XSEC_LIVE_BIN")
	if binPath == "" {
		binPath = "bin/xsec-live"
	}
	simBinPath := os.Getenv("XSEC_SIM_BIN")
	if simBinPath == "" {
		simBinPath = "bin/xsec-sim"
	}
	botBinPath := os.Getenv("XSEC_BOT_BIN")
	if botBinPath == "" {
		botBinPath = "bin/xsec-bot"
	}
	return &XSecJobRunner{
		db:         db,
		log:        log,
		binPath:    binPath,
		simBinPath: simBinPath,
		botBinPath: botBinPath,
		dbPath:     "data/trading.db",
		timeout:    xsecJobTimeout,
	}
}

// WithBinPath overrides the cmd/xsec-live subprocess path (used by tests to
// point at a fake binary, e.g. /bin/echo).
func (r *XSecJobRunner) WithBinPath(path string) *XSecJobRunner {
	r.binPath = path
	return r
}

// WithSimBinPath overrides the cmd/xsec-sim subprocess path (used by tests).
func (r *XSecJobRunner) WithSimBinPath(path string) *XSecJobRunner {
	r.simBinPath = path
	return r
}

// binPathForMode resolves which subprocess binary a job mode runs — every
// mode drives cmd/xsec-live except backtest, which is a different, read-only
// historical-simulation binary (cmd/xsec-sim) over the same candle_archive.
func (r *XSecJobRunner) binPathForMode(mode string) string {
	switch mode {
	case XSecJobModeBacktest, XSecJobModeBacktestSweep:
		return r.simBinPath
	case XSecJobModeShadowRecord:
		return r.botBinPath
	default:
		return r.binPath
	}
}

// WithTimeout overrides the hard subprocess timeout (used by tests).
func (r *XSecJobRunner) WithTimeout(d time.Duration) *XSecJobRunner {
	r.timeout = d
	return r
}

// RecoverStaleJobs marks any job left in status=running (a prior server
// instance crashed mid-job) as errored. Call once at server startup, before
// accepting new job requests.
func (r *XSecJobRunner) RecoverStaleJobs() (int64, error) {
	return xsec.MarkStaleRunningAsError(r.db)
}

// BuildArgv maps a job mode + client-supplied overrides to the exact argv
// passed to the xsec-live subprocess. This is the ONLY place client input
// influences the subprocess command line, and every value is validated
// against a fixed range/pattern before being appended — raw flags are never
// accepted from the request body. The append order for rebalance overrides
// (k → max-pos → lookback → max-total) is fixed: the dry-run-first gate
// compares argv slices for exact equality.
func (r *XSecJobRunner) BuildArgv(mode string, req XSecJobRequest) ([]string, error) {
	switch mode {
	case XSecJobModeSyncBook:
		return []string{"-db", r.dbPath, "-sync-book"}, nil

	case XSecJobModeRebalanceDry, XSecJobModeRebalanceLive:
		args := []string{"-db", r.dbPath, "-rebalance", "-target-source", "compute"}
		if req.K != nil {
			if *req.K < 1 || *req.K > 20 {
				return nil, fmt.Errorf("k out of range [1,20]: %d", *req.K)
			}
			args = append(args, "-k", strconv.Itoa(*req.K))
		}
		if req.MaxPosUSD != nil {
			if *req.MaxPosUSD < xsecMaxPosUSDMin || *req.MaxPosUSD > xsecMaxPosUSDMax {
				return nil, fmt.Errorf("max_pos_usd out of range [%g,%g]: %v", xsecMaxPosUSDMin, xsecMaxPosUSDMax, *req.MaxPosUSD)
			}
			args = append(args, "-max-pos-usd", strconv.FormatFloat(*req.MaxPosUSD, 'f', -1, 64))
		}
		if req.Lookback != nil {
			if *req.Lookback < 2 || *req.Lookback > 60 {
				return nil, fmt.Errorf("lookback out of range [2,60]: %d", *req.Lookback)
			}
			args = append(args, "-lookback", strconv.Itoa(*req.Lookback))
		}
		if req.MaxTotalUSD != nil {
			if *req.MaxTotalUSD < xsecMaxTotalUSDMin || *req.MaxTotalUSD > xsecMaxTotalUSDMax {
				return nil, fmt.Errorf("max_total_usd out of range [%g,%g]: %v", xsecMaxTotalUSDMin, xsecMaxTotalUSDMax, *req.MaxTotalUSD)
			}
			args = append(args, "-max-total-usd", strconv.FormatFloat(*req.MaxTotalUSD, 'f', -1, 64))
		}
		// -live and -i-verified-futures-sizing are added ONLY here, ONLY for the
		// live mode, and exactly once — never client-supplied, never duplicated.
		if mode == XSecJobModeRebalanceLive {
			args = append(args, "-live", "-i-verified-futures-sizing")
		}
		return args, nil

	case XSecJobModeAutoconfig:
		// Read-only sizing search: places nothing. Capital absent = xsec-live
		// reads the real futures equity from the exchange.
		args := []string{"-db", r.dbPath, "-autoconfig"}
		if req.Capital != nil {
			if *req.Capital < 10 || *req.Capital > 10_000_000 {
				return nil, fmt.Errorf("capital out of range [10,10000000]: %v", *req.Capital)
			}
			args = append(args, "-autoconfig-capital", strconv.FormatFloat(*req.Capital, 'f', -1, 64))
		}
		if req.Utilization != nil {
			if *req.Utilization <= 0 || *req.Utilization > 1 {
				return nil, fmt.Errorf("utilization out of range (0,1]: %v", *req.Utilization)
			}
			args = append(args, "-autoconfig-util", strconv.FormatFloat(*req.Utilization, 'f', -1, 64))
		}
		if req.Lookback != nil {
			if *req.Lookback < 2 || *req.Lookback > 60 {
				return nil, fmt.Errorf("lookback out of range [2,60]: %d", *req.Lookback)
			}
			args = append(args, "-lookback", strconv.Itoa(*req.Lookback))
		}
		return args, nil

	case XSecJobModeCloseAll:
		// LIQUIDATE: close every owned leg at market, then exit. De-risking only
		// (opens nothing), but gated at Level 2 since it mutates real positions.
		return []string{"-db", r.dbPath, "-close-all"}, nil

	case XSecJobModeNeutralize:
		// RE-NEUTRALIZE: close the fewest heavy-side legs to reach dollar-neutral.
		// Reduce-only, opens nothing, but mutates real positions → Level 2.
		return []string{"-db", r.dbPath, "-neutralize"}, nil

	case XSecJobModeRelaunch:
		// START OVER: liquidate everything, then re-deploy a FRESH basket in one
		// run (-close-all falls through to -rebalance against the now-empty book).
		// Opens real positions → Level 2 + UI double-confirm. Same override knobs
		// and verified-sizing guard as rebalance-live.
		args := []string{"-db", r.dbPath, "-close-all", "-rebalance", "-target-source", "compute"}
		if req.K != nil {
			if *req.K < 1 || *req.K > 20 {
				return nil, fmt.Errorf("k out of range [1,20]: %d", *req.K)
			}
			args = append(args, "-k", strconv.Itoa(*req.K))
		}
		if req.MaxPosUSD != nil {
			if *req.MaxPosUSD < xsecMaxPosUSDMin || *req.MaxPosUSD > xsecMaxPosUSDMax {
				return nil, fmt.Errorf("max_pos_usd out of range [%g,%g]: %v", xsecMaxPosUSDMin, xsecMaxPosUSDMax, *req.MaxPosUSD)
			}
			args = append(args, "-max-pos-usd", strconv.FormatFloat(*req.MaxPosUSD, 'f', -1, 64))
		}
		if req.Lookback != nil {
			if *req.Lookback < 2 || *req.Lookback > 60 {
				return nil, fmt.Errorf("lookback out of range [2,60]: %d", *req.Lookback)
			}
			args = append(args, "-lookback", strconv.Itoa(*req.Lookback))
		}
		if req.MaxTotalUSD != nil {
			if *req.MaxTotalUSD < xsecMaxTotalUSDMin || *req.MaxTotalUSD > xsecMaxTotalUSDMax {
				return nil, fmt.Errorf("max_total_usd out of range [%g,%g]: %v", xsecMaxTotalUSDMin, xsecMaxTotalUSDMax, *req.MaxTotalUSD)
			}
			args = append(args, "-max-total-usd", strconv.FormatFloat(*req.MaxTotalUSD, 'f', -1, 64))
		}
		args = append(args, "-live", "-i-verified-futures-sizing")
		return args, nil

	case XSecJobModeShadowRecord:
		// cmd/xsec-bot -shadow-only: record shadow baskets for the current date,
		// no paper-portfolio or exchange side effects. No client-tunable args.
		return []string{"-db", r.dbPath, "-shadow-only"}, nil

	case XSecJobModeBacktest:
		return r.buildBacktestArgs(req, "")

	case XSecJobModeBacktestSweep:
		if _, _, _, err := sweepParamFlag(req.SweepParam); err != nil {
			return nil, err
		}
		if len(req.SweepValues) < 1 || len(req.SweepValues) > maxSweepValues {
			return nil, fmt.Errorf("sweep_values must have between 1 and %d entries, got %d", maxSweepValues, len(req.SweepValues))
		}
		for _, v := range req.SweepValues {
			if err := validateSweepValue(req.SweepParam, v); err != nil {
				return nil, err
			}
		}
		// The base config EXCLUDES the swept dimension — execute() appends
		// each sweep value's own flag per run. Any explicit value the client
		// also sent for that same field in the base request is ignored
		// (skip=req.SweepParam), never silently combined with the sweep.
		return r.buildBacktestArgs(req, req.SweepParam)

	case XSecJobModeCancelOrder:
		if !cancelOrderIDPattern.MatchString(req.OrderID) {
			return nil, fmt.Errorf("order_id must match ^[0-9]{10,25}$, got %q", req.OrderID)
		}
		return []string{"-cancel-order", req.OrderID}, nil

	case XSecJobModeCloseLeg:
		if !pairPattern.MatchString(req.Pair) {
			return nil, fmt.Errorf("pair must match ^[A-Z0-9]{1,15}-USDT$, got %q", req.Pair)
		}
		return []string{"-db", r.dbPath, "-close-leg", req.Pair}, nil

	default:
		return nil, fmt.Errorf("unknown xsec job mode %q", mode)
	}
}

// maxSweepValues bounds backtest-sweep to a single sequential xsec-sim
// subprocess run per value — each run takes low single-digit seconds on the
// full candle_archive history, so this keeps a sweep in the range of tens of
// seconds to a few minutes without needing any concurrency.
const maxSweepValues = 24

// sweepParamFlag maps a backtest-sweep dimension name to its xsec-sim flag
// and valid range — the SAME range buildBacktestArgs enforces for that field
// individually, so a sweep can never smuggle in an out-of-range value the
// single-run form would reject.
func sweepParamFlag(param string) (flag string, min, max float64, err error) {
	switch param {
	case "lb":
		return "-lb", 2, 120, nil
	case "k":
		return "-k", 1, 20, nil
	case "rebal":
		return "-rebal", 1, 60, nil
	case "band":
		return "-band", 0, 10, nil
	default:
		return "", 0, 0, fmt.Errorf("sweep_param must be one of lb, k, rebal, band, got %q", param)
	}
}

func validateSweepValue(param string, v float64) error {
	flag, min, max, err := sweepParamFlag(param)
	if err != nil {
		return err
	}
	if v < min || v > max {
		return fmt.Errorf("sweep value out of range [%v,%v] for %s: %v", min, max, flag, v)
	}
	return nil
}

// buildBacktestArgs builds the cmd/xsec-sim argv for a backtest request,
// applying the exact same per-field validation BuildArgv's XSecJobModeBacktest
// case always has. skip, when non-empty, omits that one field ("lb", "k",
// "rebal", or "band") from the base args — used by backtest-sweep, which
// substitutes it per-run in execute() instead.
func (r *XSecJobRunner) buildBacktestArgs(req XSecJobRequest, skip string) ([]string, error) {
	args := []string{"-db", r.dbPath}
	if req.Lookback != nil && skip != "lb" {
		if *req.Lookback < 2 || *req.Lookback > 120 {
			return nil, fmt.Errorf("lookback out of range [2,120]: %d", *req.Lookback)
		}
		args = append(args, "-lb", strconv.Itoa(*req.Lookback))
	}
	if req.K != nil && skip != "k" {
		if *req.K < 1 || *req.K > 20 {
			return nil, fmt.Errorf("k out of range [1,20]: %d", *req.K)
		}
		args = append(args, "-k", strconv.Itoa(*req.K))
	}
	if req.Rebal != nil && skip != "rebal" {
		if *req.Rebal < 1 || *req.Rebal > 60 {
			return nil, fmt.Errorf("rebal out of range [1,60]: %d", *req.Rebal)
		}
		args = append(args, "-rebal", strconv.Itoa(*req.Rebal))
	}
	if req.Band != nil && skip != "band" {
		if *req.Band < 0 || *req.Band > 10 {
			return nil, fmt.Errorf("band out of range [0,10]: %d", *req.Band)
		}
		args = append(args, "-band", strconv.Itoa(*req.Band))
	}
	if req.Cost != nil {
		if *req.Cost < 0 || *req.Cost > 0.02 {
			return nil, fmt.Errorf("cost out of range [0,0.02]: %v", *req.Cost)
		}
		args = append(args, "-cost", strconv.FormatFloat(*req.Cost, 'f', -1, 64))
	}
	if req.Funding != nil {
		if *req.Funding < -0.01 || *req.Funding > 0.01 {
			return nil, fmt.Errorf("funding out of range [-0.01,0.01]: %v", *req.Funding)
		}
		args = append(args, "-funding", strconv.FormatFloat(*req.Funding, 'f', -1, 64))
	}
	if req.Capital != nil {
		if *req.Capital < 10 || *req.Capital > 1_000_000 {
			return nil, fmt.Errorf("capital out of range [10,1000000]: %v", *req.Capital)
		}
		args = append(args, "-capital", strconv.FormatFloat(*req.Capital, 'f', -1, 64))
	}
	if req.VolWeights != nil {
		// A bool flag MUST be a single "-flag=value" token, never two
		// separate argv elements ("-flag", "value") — Go's flag package
		// treats a bare "-vol-weights" as already satisfied (implicitly
		// true) and the next token as the first POSITIONAL argument,
		// silently ending flag parsing right there. Every flag appended
		// after this one in argv would then be silently ignored (found
		// 2026-07-23: min-turnover-usd/windows/print-equity all never
		// took effect on any backtest with vol-weights set — i.e. every
		// backtest run so far, since the UI always sends this field).
		args = append(args, "-vol-weights="+strconv.FormatBool(*req.VolWeights))
	}
	if req.MinTurnoverUSD != nil {
		if *req.MinTurnoverUSD < 0 || *req.MinTurnoverUSD > 50_000_000 {
			return nil, fmt.Errorf("min_turnover_usd out of range [0,50000000]: %v", *req.MinTurnoverUSD)
		}
		args = append(args, "-min-turnover-usd", strconv.FormatFloat(*req.MinTurnoverUSD, 'f', -1, 64))
	}
	if req.Windows != nil {
		if *req.Windows < 1 || *req.Windows > 12 {
			return nil, fmt.Errorf("windows out of range [1,12]: %d", *req.Windows)
		}
		args = append(args, "-windows", strconv.Itoa(*req.Windows))
	}

	// Advanced (Fase 3) — execution realism, intra-period risk, OOS hold-out.
	if req.StopShort != nil {
		if *req.StopShort < 0 || *req.StopShort > 0.5 {
			return nil, fmt.Errorf("stop_short out of range [0,0.5]: %v", *req.StopShort)
		}
		args = append(args, "-stop-short", strconv.FormatFloat(*req.StopShort, 'f', -1, 64))
	}
	if req.StopLong != nil {
		if *req.StopLong < 0 || *req.StopLong > 0.5 {
			return nil, fmt.Errorf("stop_long out of range [0,0.5]: %v", *req.StopLong)
		}
		args = append(args, "-stop-long", strconv.FormatFloat(*req.StopLong, 'f', -1, 64))
	}
	if req.TPLiquidate != nil {
		if *req.TPLiquidate < 0 || *req.TPLiquidate > 0.2 {
			return nil, fmt.Errorf("tp_liquidate out of range [0,0.2]: %v", *req.TPLiquidate)
		}
		args = append(args, "-tp-liquidate", strconv.FormatFloat(*req.TPLiquidate, 'f', -1, 64))
	}
	if req.TPLeg != nil {
		if *req.TPLeg < 0 || *req.TPLeg > 1.0 {
			return nil, fmt.Errorf("tp_leg out of range [0,1.0]: %v", *req.TPLeg)
		}
		args = append(args, "-tp-leg", strconv.FormatFloat(*req.TPLeg, 'f', -1, 64))
	}
	if req.TPRebalance != nil {
		if *req.TPRebalance < 0 || *req.TPRebalance > 0.5 {
			return nil, fmt.Errorf("tp_rebalance out of range [0,0.5]: %v", *req.TPRebalance)
		}
		args = append(args, "-tp-rebalance", strconv.FormatFloat(*req.TPRebalance, 'f', -1, 64))
	}
	if req.TrailTP != nil {
		if *req.TrailTP < 0 || *req.TrailTP > 0.5 {
			return nil, fmt.Errorf("trail_tp out of range [0,0.5]: %v", *req.TrailTP)
		}
		args = append(args, "-trail-tp", strconv.FormatFloat(*req.TrailTP, 'f', -1, 64))
	}
	if req.RegimeFloor != nil {
		if *req.RegimeFloor < 0 || *req.RegimeFloor > 1.0 {
			return nil, fmt.Errorf("regime_floor out of range [0,1.0]: %v", *req.RegimeFloor)
		}
		args = append(args, "-regime-floor", strconv.FormatFloat(*req.RegimeFloor, 'f', -1, 64))
	}
	if req.RegimeWindow != nil {
		if *req.RegimeWindow < 2 || *req.RegimeWindow > 60 {
			return nil, fmt.Errorf("regime_window out of range [2,60]: %d", *req.RegimeWindow)
		}
		args = append(args, "-regime-window", strconv.Itoa(*req.RegimeWindow))
	}
	if req.MakerFillProb != nil {
		if *req.MakerFillProb < 0 || *req.MakerFillProb > 1 {
			return nil, fmt.Errorf("maker_fill_prob out of range [0,1]: %v", *req.MakerFillProb)
		}
		args = append(args, "-maker-fill-prob", strconv.FormatFloat(*req.MakerFillProb, 'f', -1, 64))
	}
	if req.TakerCost != nil {
		if *req.TakerCost < 0 || *req.TakerCost > 0.02 {
			return nil, fmt.Errorf("taker_cost out of range [0,0.02]: %v", *req.TakerCost)
		}
		args = append(args, "-taker-cost", strconv.FormatFloat(*req.TakerCost, 'f', -1, 64))
	}
	if req.VolTarget != nil {
		if *req.VolTarget < 0 || *req.VolTarget > 3 {
			return nil, fmt.Errorf("vol_target out of range [0,3]: %v", *req.VolTarget)
		}
		args = append(args, "-vol-target", strconv.FormatFloat(*req.VolTarget, 'f', -1, 64))
	}
	if req.MaxShortWeight != nil {
		if *req.MaxShortWeight < 0 || *req.MaxShortWeight > 1 {
			return nil, fmt.Errorf("max_short_weight out of range [0,1]: %v", *req.MaxShortWeight)
		}
		args = append(args, "-max-short-weight", strconv.FormatFloat(*req.MaxShortWeight, 'f', -1, 64))
	}
	if req.FundingTilt != nil {
		if *req.FundingTilt < -1 || *req.FundingTilt > 1 {
			return nil, fmt.Errorf("funding_tilt out of range [-1,1]: %v", *req.FundingTilt)
		}
		args = append(args, "-funding-tilt", strconv.FormatFloat(*req.FundingTilt, 'f', -1, 64))
	}
	if req.Pairs != "" {
		for _, p := range strings.Split(req.Pairs, ",") {
			p = strings.TrimSpace(p)
			if !pairPattern.MatchString(p) {
				return nil, fmt.Errorf("pairs entry must match ^[A-Z0-9]{1,15}-USDT$, got %q", p)
			}
		}
		args = append(args, "-pairs", req.Pairs)
	}
	if req.StartFrac != nil {
		if *req.StartFrac < 0 || *req.StartFrac > 1 {
			return nil, fmt.Errorf("start_frac out of range [0,1]: %v", *req.StartFrac)
		}
		args = append(args, "-start-frac", strconv.FormatFloat(*req.StartFrac, 'f', -1, 64))
	}
	if req.EndFrac != nil {
		if *req.EndFrac < 0 || *req.EndFrac > 1 {
			return nil, fmt.Errorf("end_frac out of range [0,1]: %v", *req.EndFrac)
		}
		args = append(args, "-end-frac", strconv.FormatFloat(*req.EndFrac, 'f', -1, 64))
	}
	if req.StartFrac != nil && req.EndFrac != nil && *req.StartFrac >= *req.EndFrac {
		return nil, fmt.Errorf("start_frac (%v) must be less than end_frac (%v)", *req.StartFrac, *req.EndFrac)
	}

	// Always requested, never client-controlled — the UI always wants the
	// equity curve to chart, and it's cheap (per-rebalance points only).
	args = append(args, "-print-equity")
	return args, nil
}

// checkDryRunFirst reports whether a rebalance-live request with the given
// overrides may proceed: there must be a rebalance-dry job that finished
// status=done, with IDENTICAL overrides (k/max_pos_usd/lookback/max_total_usd),
// within the last dryRunFirstWindow. Reuses BuildArgv with the FULL request so
// the compared argv can never drift between the dry and live code paths — a
// live request differing from the dry run in ANY override must not pass.
func (r *XSecJobRunner) checkDryRunFirst(req XSecJobRequest) (bool, error) {
	dryArgs, err := r.BuildArgv(XSecJobModeRebalanceDry, req)
	if err != nil {
		return false, err
	}
	_, found, err := xsec.FindRecentJob(r.db, XSecJobModeRebalanceDry, xsec.JobStatusDone, dryArgs, dryRunFirstWindow)
	if err != nil {
		return false, err
	}
	return found, nil
}

// Start validates req, enforces the single-flight and dry-run-first guards,
// creates the job row, and spawns the subprocess asynchronously. It returns
// as soon as the job row exists (does not wait for the subprocess).
func (r *XSecJobRunner) Start(req XSecJobRequest, createdBy string) (int64, error) {
	switch req.Mode {
	case XSecJobModeSyncBook, XSecJobModeRebalanceDry, XSecJobModeRebalanceLive, XSecJobModeCancelOrder, XSecJobModeCloseLeg, XSecJobModeBacktest, XSecJobModeBacktestSweep, XSecJobModeShadowRecord, XSecJobModeCloseAll, XSecJobModeRelaunch, XSecJobModeNeutralize, XSecJobModeAutoconfig:
	default:
		return 0, &XSecJobStartError{Status: 400, Message: fmt.Sprintf("invalid mode %q", req.Mode)}
	}

	if killswitch.Ring0Active() && xsecModeIncreasesRisk(req.Mode) {
		return 0, &XSecJobStartError{
			Status:  http.StatusLocked,
			Code:    killswitch.ErrorCode,
			Message: fmt.Sprintf("global kill switch (ring 0) is active: mode %q can open or increase positions and is blocked", req.Mode),
		}
	}

	argv, err := r.BuildArgv(req.Mode, req)
	if err != nil {
		return 0, &XSecJobStartError{Status: 400, Message: err.Error()}
	}

	if req.Mode == XSecJobModeRebalanceLive {
		ok, err := r.checkDryRunFirst(req)
		if err != nil {
			return 0, fmt.Errorf("xsec job runner: dry-run-first check: %w", err)
		}
		if !ok {
			return 0, &XSecJobStartError{
				Status: 412,
				Message: "rebalance-live requires a completed rebalance-dry job with identical " +
					"k/max_pos_usd/lookback/max_total_usd overrides within the last 10 minutes",
			}
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	running, isRunning, err := xsec.AnyJobRunning(r.db)
	if err != nil {
		return 0, fmt.Errorf("xsec job runner: single-flight check: %w", err)
	}
	if isRunning {
		return 0, &XSecJobStartError{
			Status:       409,
			Message:      "an xsec job is already running",
			RunningJobID: running.ID,
		}
	}

	jobID, err := xsec.CreateJob(r.db, req.Mode, argv, createdBy)
	if err != nil {
		return 0, fmt.Errorf("xsec job runner: create job: %w", err)
	}

	go r.execute(jobID, req.Mode, argv, sweepSpecFor(req))

	return jobID, nil
}

// syncBuffer is an io.Writer that serializes concurrent writes (subprocess
// Stdout+Stderr go to the same buffer) behind a mutex the flush goroutine
// also reads under.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// sweepSpec carries a backtest-sweep's resolved dimension (xsec-sim flag
// name) and the values to run it at — one subprocess invocation per value,
// see executeSweep. nil for every other job mode.
type sweepSpec struct {
	flag   string
	values []float64
}

// sweepSpecFor returns a *sweepSpec for a backtest-sweep request, nil for
// every other mode. The sweep_param validity was already enforced by
// BuildArgv before Start() ever reaches this point — the error return here
// is defensive only (never expected to fire on a request that got this far).
func sweepSpecFor(req XSecJobRequest) *sweepSpec {
	if req.Mode != XSecJobModeBacktestSweep {
		return nil
	}
	flag, _, _, err := sweepParamFlag(req.SweepParam)
	if err != nil {
		return nil
	}
	return &sweepSpec{flag: flag, values: req.SweepValues}
}

// execute runs the subprocess(es) for jobID under one shared r.timeout
// deadline, then marks the job done or error. Runs in its own goroutine,
// detached from the HTTP request that created the job. sweep is non-nil only
// for XSecJobModeBacktestSweep, which runs cmd/xsec-sim once per sweep value
// instead of once for the whole job — see executeSweep.
func (r *XSecJobRunner) execute(jobID int64, mode string, argv []string, sweep *sweepSpec) {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	if sweep != nil {
		r.executeSweep(ctx, jobID, mode, argv, sweep)
		return
	}

	output, exitCode, runErr := r.runSubprocess(ctx, r.binPathForMode(mode), argv, func(partial string) {
		if err := xsec.UpdateJobOutput(r.db, jobID, partial); err != nil {
			r.log.WithError(err).WithField("job_id", jobID).Warn("xsec job runner: periodic output flush failed")
		}
	})

	status := xsec.JobStatusDone
	if runErr != nil {
		status = xsec.JobStatusError
	}
	if err := xsec.FinishJob(r.db, jobID, status, exitCode, output); err != nil {
		r.log.WithError(err).WithField("job_id", jobID).Error("xsec job runner: failed to finalize job row")
	}
}

// executeSweep runs cmd/xsec-sim once per sweep.values, appending
// sweep.flag=<value> to the shared base argv each time, and concatenates
// every run's full stdout/stderr into the job's output separated by a
// "### SWEEP i=<n> <param>=<value> ###" header — parseBacktestSweep on the
// frontend splits on that exact marker. Progress is flushed to the DB after
// EACH run completes (not just periodically), so JobMonitor's poll shows the
// sweep filling in one result at a time. One run failing (e.g. an
// out-of-range config for the current universe) does not abort the rest —
// only if the shared deadline is hit does the sweep stop early, and only if
// EVERY run failed is the job marked error.
func (r *XSecJobRunner) executeSweep(ctx context.Context, jobID int64, mode string, baseArgv []string, sweep *sweepSpec) {
	binPath := r.binPathForMode(mode)
	var combined bytes.Buffer
	anySuccess := false

	for i, v := range sweep.values {
		runArgv := make([]string, len(baseArgv), len(baseArgv)+2)
		copy(runArgv, baseArgv)
		runArgv = append(runArgv, sweep.flag, strconv.Itoa(int(v)))

		fmt.Fprintf(&combined, "### SWEEP i=%d %s=%v ###\n", i+1, strings.TrimPrefix(sweep.flag, "-"), v)
		output, _, runErr := r.runSubprocess(ctx, binPath, runArgv, nil)
		combined.WriteString(output)
		combined.WriteString("\n")
		if runErr == nil {
			anySuccess = true
		}

		if err := xsec.UpdateJobOutput(r.db, jobID, combined.String()); err != nil {
			r.log.WithError(err).WithField("job_id", jobID).Warn("xsec job runner: sweep progress flush failed")
		}
		if ctx.Err() != nil {
			combined.WriteString("[runner] sweep stopped: shared job timeout reached\n")
			break
		}
	}

	status := xsec.JobStatusDone
	if !anySuccess {
		status = xsec.JobStatusError
	}
	// exit_code has no single meaningful value across N runs — always 0;
	// per-run failures are visible in each run's own embedded [runner] note.
	if err := xsec.FinishJob(r.db, jobID, status, 0, combined.String()); err != nil {
		r.log.WithError(err).WithField("job_id", jobID).Error("xsec job runner: failed to finalize sweep job row")
	}
}

// runSubprocess runs one subprocess to completion under ctx's deadline,
// capturing combined stdout+stderr. onOutput, if non-nil, is called on a
// ticker with the buffer's contents-so-far while the subprocess is still
// running (periodic progress flush) — callers that flush their own way
// (e.g. executeSweep, once per completed run) pass nil.
func (r *XSecJobRunner) runSubprocess(ctx context.Context, binPath string, argv []string, onOutput func(string)) (output string, exitCode int, runErr error) {
	cmd := exec.CommandContext(ctx, binPath, argv...)
	// Inherit the server process environment — under systemd this already
	// carries GCT_ADDRESS/GCT_USERNAME/GCT_PASSWORD/KUCOIN_* credentials.
	cmd.Env = os.Environ()

	out := &syncBuffer{}
	cmd.Stdout = io.Writer(out)
	cmd.Stderr = io.Writer(out)

	var flushDone chan struct{}
	if onOutput != nil {
		flushDone = make(chan struct{})
		go func() {
			ticker := time.NewTicker(xsecJobFlushInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					onOutput(out.String())
				case <-flushDone:
					return
				}
			}
		}()
	}

	runErr = cmd.Run()
	if flushDone != nil {
		close(flushDone)
	}

	output = out.String()
	exitCode = 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if ok := isExitError(runErr, &exitErr); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
			output += fmt.Sprintf("\n[runner] %v\n", runErr)
			if ctx.Err() == context.DeadlineExceeded {
				output += "[runner] job exceeded the hard timeout and was killed\n"
			}
		}
	}
	return output, exitCode, runErr
}

// isExitError is a small indirection around errors.As so execute's control
// flow reads linearly; separated mainly for testability of the branch.
func isExitError(err error, target **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*target = ee
		return true
	}
	return false
}
