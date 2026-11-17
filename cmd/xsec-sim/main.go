// Command xsec-sim is the REALISTIC portfolio backtest for the cross-sectional
// momentum factor (the best lead — see project_xsec_momentum_lead). xsec-factor
// measured the abstract long-short spread; this models the actual market-neutral
// portfolio: fixed capital, real long/short baskets, transaction cost charged
// ONLY on turnover (pairs that rotate in/out — not a full rebalance every period),
// and funding accrual on BOTH legs (longs pay, shorts receive — so net funding
// largely cancels in a balanced book). It is the same discipline that exposed the
// daily-trend illusion: per-period factor returns ≠ a real equity curve.
//
// Research-grade upgrades (F0.2+F0.3, all backward-compatible):
//
//   - -min-turnover-usd: per-rebalance HISTORICAL universe (anti-survivorship).
//     At each rebalance only pairs whose trailing 7-bar avg daily turnover
//     (volume*close) exceeds the threshold are eligible — the universe is
//     time-varying like reality.
//
//   - -funding-archive: per-pair historical funding accrual from table
//     funding_archive(pair, ts, rate). Long pays +rate, short receives +rate:
//     pnl_funding = -dir * rate_sum_day * notional. Missing pair-days fall
//     back to the scalar -funding drag. Degrades gracefully if the table
//     does not exist yet.
//
//   - -csv-equity / -csv-trades: CSV output of the equity curve and rotations.
//
//     go run ./cmd/xsec-sim -lb 14 -k 3 -rebal 7 -cost 0.0012 -funding 0.0001
package main

import (
	"bytes"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/storage"
	"github.com/rantipay/trading-mcp/internal/xsec"
)

var (
	dbPath  = flag.String("db", "data/trading.db", "SQLite candle_archive path")
	tf      = flag.String("tf", "1d", "timeframe")
	lb      = flag.Int("lb", 14, "momentum lookback in bars")
	k       = flag.Int("k", 3, "basket size each side")
	rebal   = flag.Int("rebal", 7, "rebalance period in bars")
	band    = flag.Int("band", 0, "no-trade band (hysteresis): keep an incumbent name until its rank leaves the top/bottom (k+band); 0 = plain top-k/bottom-k")
	cost    = flag.Float64("cost", 0.0012, "round-trip cost fraction per rotated position (0.0012=0.12%), charged when the maker (post-only) order fills")
	funding = flag.Float64("funding", 0.0001, "NET daily funding drag on the book (0.0001=0.01%/day; longs pay - shorts receive)")

	// F2 (XSEC execution plan): model maker/no-fill risk instead of assuming
	// every rotation order fills at the post-only (maker) price. The flat -cost
	// model overstates the maker edge, since real post-only orders sometimes
	// miss the rebalance window (price moves away before the order crosses).
	makerFillProb = flag.Float64("maker-fill-prob", 1.0, "probability a post-only rotation order fills at maker cost within the rebalance window (1.0 = legacy behavior, every order fills at -cost)")
	takerCost     = flag.Float64("taker-cost", 0.0024, "round-trip cost fraction charged when a rotation order does NOT fill at maker within the window and falls back to taker (only used when -maker-fill-prob < 1.0)")
	capital       = flag.Float64("capital", 1000, "starting capital")
	pairsArg      = flag.String("pairs", "", "restrict to these comma pairs (empty = all in archive)")

	minTurnoverUSD = flag.Float64("min-turnover-usd", 0, "per-rebalance eligibility: trailing 7-bar avg daily turnover (volume*close) must exceed this (0 = off)")
	multCache      = flag.String("multipliers-cache", "", "JSON cache of pair->contract multiplier: load if the file exists (no HTTP), else fetch from KuCoin and write it. Avoids the flaky contracts/active call on every -min-turnover-usd run (0 = always fetch, no cache)")
	useFundingArch = flag.Bool("funding-archive", false, "accrue per-pair historical funding from table funding_archive (fallback to scalar -funding for missing pair-days)")
	csvEquityPath  = flag.String("csv-equity", "", "write equity curve CSV (date,equity,nLong,nShort,grossExposure) to this path")
	csvTradesPath  = flag.String("csv-trades", "", "write rotation trades CSV (date,pair,side,action,price,notional,pnl) to this path")

	// F1 risk overlays (all default off = legacy behavior).
	volWeights     = flag.Bool("vol-weights", false, "inverse-volatility weights within each leg (sigma = stddev of daily log returns over last 20 bars)")
	maxShortWeight = flag.Float64("max-short-weight", 0, "cap any single SHORT-leg weight at this fraction of the leg, redistributing excess to other shorts (0 = off)")
	volTarget      = flag.Float64("vol-target", 0, "annualized portfolio vol target; scale gross exposure by clamp(target/realized,0.25,1.5) using EWMA(half-life 12d) of simulated daily returns (0 = off)")
	fundingTilt    = flag.Float64("funding-tilt", 0, "momentum score tilt: score' = score - tilt*funding_daily_sum(pair, rebalance day) from -funding-archive (0 = off)")

	// F2 intra-period single-name stops (default off = legacy behavior).
	// On each DAILY bar strictly between rebalances, if an open position's
	// adverse move since its period entry exceeds the threshold, it is closed
	// at that bar's close, pays -cost on its notional, stops accruing funding,
	// and its capital sits in cash until the next rebalance. Stops re-arm at
	// every rebalance (entry price resets each period).
	stopShort = flag.Float64("stop-short", 0, "intra-period stop for SHORTS: close when price rises >= this fraction above period entry (0 = off)")
	stopLong  = flag.Float64("stop-long", 0, "intra-period stop for LONGS: close when price falls >= this fraction below period entry (0 = off)")

	// Portfolio-level take-profit liquidation ("close everything when winning,
	// restart next rebalance"). On each DAILY bar strictly between rebalances,
	// if the BOOK's cumulative period PnL (realized stops + mark-to-market of
	// open legs, as a fraction of deployed capital) reaches the threshold, ALL
	// open legs are closed at that bar's close, each paying the round-trip
	// -cost on its notional (covers the early exit + eventual re-open, same
	// convention as single-name stops), and the book sits flat until the next
	// rebalance re-ranks and re-opens fresh.
	tpLiq = flag.Float64("tp-liquidate", 0, "portfolio take-profit: liquidate the WHOLE book when its period PnL fraction >= this (e.g. 0.01 = +1%), flat until next rebalance (0 = off)")

	// Per-leg take-profit: harvest a SINGLE winning leg intra-period (the "take
	// the pair that is winning" idea) without touching the rest of the book. On
	// each DAILY bar strictly between rebalances, if an open position's own
	// return since period entry (dir*(c/entry-1)) reaches the threshold, it is
	// closed at that bar's close, pays the round-trip -cost on its notional
	// (early exit + eventual re-open, same convention as stops), banks its gain,
	// stops accruing funding, and its slice sits in cash until the next
	// rebalance. Fixed cadence — directly comparable to the baseline. Symmetric
	// to -stop-long/-stop-short but on the profit side.
	tpLeg = flag.Float64("tp-leg", 0, "per-leg take-profit: close an INDIVIDUAL leg when its own return >= this (e.g. 0.15 = +15%), that slice to cash until next rebalance (0 = off)")

	// Triggered early rebalance ("re-run the basket when winning"): the dynamic
	// cadence version of -tp-liquidate. On each DAILY bar strictly between
	// rebalances, if the BOOK's cumulative period PnL fraction reaches the
	// threshold, the whole book is closed at that bar's close (each leg paying
	// round-trip -cost) and the outer loop RE-RANKS AND RE-DEPLOYS immediately
	// at that bar instead of waiting for the fixed -rebal cadence. Because this
	// makes the holding period variable, Sharpe is annualized by the realized
	// average period length rather than the fixed -rebal (CAGR and max-DD are
	// already cadence-agnostic). 0 = off (fixed cadence, legacy).
	tpRebal = flag.Float64("tp-rebalance", 0, "triggered early rebalance: when book period PnL fraction >= this, re-rank and re-deploy immediately instead of waiting for -rebal (0 = off)")

	// Trailing take-profit ("let the winner run, take profit when the trend
	// turns"): within a holding period, track the book's PEAK PnL fraction; once
	// that peak is positive, liquidate the whole book when the current PnL has
	// retraced this many points BELOW the peak (e.g. 0.03 = gave back 3pp from
	// the best point), flat until the next rebalance reloads a fresh basket.
	// Unlike -tp-liquidate (fixed absolute level) this is PATH-DEPENDENT: it lets
	// a winner keep running to any height and only exits on a reversal from the
	// peak. Tests the "was at $2, now $1.70 — should have taken profit" idea.
	trailTP = flag.Float64("trail-tp", 0, "trailing take-profit: liquidate when book PnL retraces this many points below its positive intra-period peak (0 = off)")

	// (b) Regime gate on cross-sectional momentum DISPERSION — the theoretically
	// motivated regime signal for a cross-sectional momentum book: when the
	// universe's momentum scores are widely spread (high dispersion) there is
	// more winner/loser separation to harvest; when they bunch up, less edge.
	// At each rebalance the stdev of the eligible universe's momentum signals is
	// compared to the median of its own trailing history (last -regime-window
	// rebalances, PAST only — no look-ahead); when current dispersion is below
	// that median the book is deployed at -regime-floor scale instead of full.
	// Uses only information available at decision time.
	regimeFloor  = flag.Float64("regime-floor", 1.0, "exposure scale applied when cross-sectional momentum dispersion is below its trailing median (1.0 = gate off)")
	regimeWindow = flag.Int("regime-window", 10, "trailing window (in rebalances) for the dispersion-median regime gate")

	// OOS hold-out: restrict the whole run to a sub-range [start-frac, end-frac]
	// of the full chronological timeline. Lets you SELECT a config on the early
	// in-sample slice (e.g. -end-frac 0.7) and then evaluate it BLIND on the
	// untouched tail (-start-frac 0.7). The lookback at the slice boundary stays
	// inside the slice, so the OOS run never peeks into the in-sample data.
	startFrac = flag.Float64("start-frac", 0.0, "use only the timeline from this fraction onward (0..1)")
	endFrac   = flag.Float64("end-frac", 1.0, "use only the timeline up to this fraction (0..1)")
	windows   = flag.Int("windows", 4, "number of sequential walk-forward sub-periods")

	// printEquity emits the same per-rebalance equity curve -csv-equity writes
	// to a file, instead/in addition, as a fenced block on stdout — for a
	// caller (e.g. the trading-mcp job runner) that captures stdout only and
	// has no access to the process's local filesystem for a separate file.
	printEquity = flag.Bool("print-equity", false, "emit the equity curve as a fenced CSV block on stdout (## EQUITY_CSV_BEGIN/END), in addition to -csv-equity if also set")
)

// pairData holds per-pair series indexed by candle timestamp (unix seconds).
type pairData struct {
	close    map[int64]float64
	turnover map[int64]float64 // volume * close (USD turnover per bar)
}

// simConfig carries everything simulate needs beyond the global flags.
type simConfig struct {
	fundingByPairDay map[string]map[int64]float64 // pair -> day(unix, floor 86400) -> sum of funding rates that day
	csvEquity        *csv.Writer
	csvTrades        *csv.Writer
}

// simStats is the result bundle from one simulate run.
type simStats struct {
	eq, cagr, sharpe, maxDD, totRet float64
	nReb                            int
	avgTurn                         float64
	fundingPaidUSD                  float64 // sum of negative funding pnl (cost), reported positive
	fundingRecvUSD                  float64 // sum of positive funding pnl (gain)
	avgUniverse                     float64 // avg eligible universe size per rebalance attempt
	nShrunk                         int     // rebalances where eligible universe < 2k (skipped)
	nStops                          int     // intra-period stops fired
	stopRetSum                      float64 // sum of position returns at stop (negative = loss)
	nTPLiq                          int     // portfolio take-profit liquidations fired
	tpRetSum                        float64 // sum of book period-PnL fractions at liquidation
	nTPLeg                          int     // per-leg take-profits fired
	tpLegRetSum                     float64 // sum of position returns at per-leg take-profit
	nTPRebal                        int     // triggered early rebalances fired
	tpRebalRetSum                   float64 // sum of book period-PnL fractions at early rebalance
	nTrailTP                        int     // trailing take-profits fired
	trailPeakSum                    float64 // sum of book peak PnL at each trailing exit
	nRegimeGated                    int     // rebalances the dispersion regime gate de-risked
	avgPeriodBars                   float64 // realized average holding period in bars (== rebal unless -tp-rebalance)
	liquidated                      bool    // equity hit the zero floor (account wiped out); simulation stopped there
	liquidatedAt                    int64   // unix ts of the period end where the account was liquidated
}

// applyPeriodReturn compounds one holding period's net return into equity with
// an account-level floor at zero: a market-neutral book can lose more than 100%
// in one period (an unstopped short in a pump), but a real account cannot go
// negative — it is liquidated. Returns the new equity and whether it was wiped.
func applyPeriodReturn(equity, net float64) (float64, bool) {
	if 1+net <= 0 {
		return 0, true
	}
	return equity * (1 + net), false
}

// verdictFor classifies a run as PROMISING / MARGINAL / NEGATIVE from the full
// run and its walk-forward windows. Any liquidation (full run or a window)
// disqualifies PROMISING; a liquidated full run has CAGR -100% → NEGATIVE.
func verdictFor(full simStats, windows []simStats) string {
	allPos := true
	anyLiq := full.liquidated
	for _, w := range windows {
		if w.totRet <= 0 {
			allPos = false
		}
		if w.liquidated {
			anyLiq = true
		}
	}
	switch {
	case full.liquidated:
		return "NEGATIVE"
	case !anyLiq && full.cagr > 0.10 && full.sharpe > 0.6 && full.maxDD < 0.45 && allPos:
		return "PROMISING"
	case full.cagr > 0:
		return "MARGINAL"
	default:
		return "NEGATIVE"
	}
}

func main() {
	flag.Parse()
	_ = godotenv.Load()
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	db, err := sql.Open("sqlite3", *dbPath+"?_journal_mode=WAL")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer db.Close()
	archive := storage.NewCandleArchiveRepository(db, log)

	rows, _ := db.Query("SELECT DISTINCT pair FROM candle_archive WHERE timeframe=? ORDER BY pair", *tf)
	var pairs []string
	for rows.Next() {
		var p string
		rows.Scan(&p)
		pairs = append(pairs, p)
	}
	rows.Close()

	if *pairsArg != "" {
		want := map[string]bool{}
		for _, p := range splitCSVlocal(*pairsArg) {
			want[p] = true
		}
		var filtered []string
		for _, p := range pairs {
			if want[p] {
				filtered = append(filtered, p)
			}
		}
		pairs = filtered
	}

	// Candle volume for KuCoin futures pairs is denominated in CONTRACTS, and
	// contract multipliers vary wildly per pair (SOL=0.1, ADA=10, DOGE=100).
	// Raw volume*close therefore misestimates USD turnover by the multiplier,
	// which silently distorts the -min-turnover-usd universe filter. Fetch the
	// live multiplier map when the filter is on; fall back to 1.0 with a
	// warning if the API is unreachable.
	multipliers := map[string]float64{}
	if *minTurnoverUSD > 0 {
		// Prefer a local JSON cache to avoid the flaky KuCoin contracts/active
		// call (observed 1.3s..10s+, occasionally hanging) on every run of a
		// sweep. Load if the cache file exists; otherwise fetch once and write
		// it so the next run is instant.
		loaded := false
		if *multCache != "" {
			if raw, rerr := os.ReadFile(*multCache); rerr == nil {
				if jerr := json.Unmarshal(raw, &multipliers); jerr == nil && len(multipliers) > 0 {
					fmt.Printf("# Contract multipliers loaded from cache %s (%d pairs)\n", *multCache, len(multipliers))
					loaded = true
				}
			}
		}
		if !loaded {
			var err error
			multipliers, err = fetchContractMultipliers()
			if err != nil {
				fmt.Fprintf(os.Stderr, "WARN: contract multipliers unavailable (%v) — turnover uses raw volume*close\n", err)
				multipliers = map[string]float64{}
			} else {
				fmt.Printf("# Contract multipliers loaded for %d pairs (turnover = volume*multiplier*close)\n", len(multipliers))
				if *multCache != "" && len(multipliers) > 0 {
					if raw, jerr := json.Marshal(multipliers); jerr == nil {
						if werr := os.WriteFile(*multCache, raw, 0o644); werr != nil {
							fmt.Fprintf(os.Stderr, "WARN: could not write multipliers cache %s: %v\n", *multCache, werr)
						}
					}
				}
			}
		}
	}

	data := map[string]*pairData{}
	allTS := map[int64]bool{}
	for _, p := range pairs {
		raw, err := archive.LoadRange(p, *tf, time.Time{}, time.Now())
		if err != nil || len(raw) < 120 {
			continue
		}
		mult := multipliers[p]
		if mult <= 0 {
			mult = 1.0
		}
		pd := &pairData{close: map[int64]float64{}, turnover: map[int64]float64{}}
		for _, c := range raw {
			ts := c.Timestamp.Unix()
			pd.close[ts] = c.Close
			pd.turnover[ts] = c.Volume * mult * c.Close
			allTS[ts] = true
		}
		data[p] = pd
	}
	var timeline []int64
	for ts := range allTS {
		timeline = append(timeline, ts)
	}
	sort.Slice(timeline, func(i, j int) bool { return timeline[i] < timeline[j] })

	// OOS hold-out: clamp the timeline to [start-frac, end-frac] before any
	// simulation. Everything downstream (main run + walk-forward) sees only the
	// sub-range, so selection on one slice and evaluation on another are cleanly
	// separated.
	if *startFrac > 0 || *endFrac < 1.0 {
		n := len(timeline)
		lo := int(*startFrac * float64(n))
		hi := int(*endFrac * float64(n))
		if lo < 0 {
			lo = 0
		}
		if hi > n {
			hi = n
		}
		if hi-lo < 2**lb+*rebal+2 {
			fmt.Fprintf(os.Stderr, "ERROR: [start-frac %.2f, end-frac %.2f] leaves only %d bars — too few for lb=%d rebal=%d\n",
				*startFrac, *endFrac, hi-lo, *lb, *rebal)
			os.Exit(1)
		}
		timeline = timeline[lo:hi]
		fmt.Printf("# OOS hold-out: timeline restricted to [%.2f, %.2f] → %d bars (%s .. %s)\n",
			*startFrac, *endFrac, len(timeline),
			time.Unix(timeline[0], 0).UTC().Format("2006-01-02"),
			time.Unix(timeline[len(timeline)-1], 0).UTC().Format("2006-01-02"))
	}
	years := float64(timeline[len(timeline)-1]-timeline[0]) / (365.25 * 24 * 3600)

	cfg := simConfig{}
	if *useFundingArch {
		cfg.fundingByPairDay = loadFundingArchive(db)
	}
	var eqFile, trFile *os.File
	var eqBuf *bytes.Buffer
	if *csvEquityPath != "" || *printEquity {
		var writers []io.Writer
		if *csvEquityPath != "" {
			eqFile, err = os.Create(*csvEquityPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, "csv-equity:", err)
				os.Exit(1)
			}
			defer eqFile.Close()
			writers = append(writers, eqFile)
		}
		if *printEquity {
			eqBuf = &bytes.Buffer{}
			writers = append(writers, eqBuf)
		}
		cfg.csvEquity = csv.NewWriter(io.MultiWriter(writers...))
		cfg.csvEquity.Write([]string{"date", "equity", "nLong", "nShort", "grossExposure"})
	}
	if *csvTradesPath != "" {
		trFile, err = os.Create(*csvTradesPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "csv-trades:", err)
			os.Exit(1)
		}
		defer trFile.Close()
		cfg.csvTrades = csv.NewWriter(trFile)
		cfg.csvTrades.Write([]string{"date", "pair", "side", "action", "price", "notional", "pnl"})
		defer cfg.csvTrades.Flush()
	}

	st := simulate(data, timeline, years, cfg)

	if cfg.csvEquity != nil {
		cfg.csvEquity.Flush()
	}
	if eqBuf != nil {
		fmt.Println("## EQUITY_CSV_BEGIN")
		fmt.Print(eqBuf.String())
		fmt.Println("## EQUITY_CSV_END")
	}

	fmt.Printf("# Cross-sectional MOMENTUM — realistic market-neutral PORTFOLIO sim\n")
	fmt.Printf("# %s, %d pairs, %.1f years | lb=%d k=%d band=%d rebal=%d cost=%.3f%%/rotation funding=%.3f%%/day\n",
		*tf, len(data), years, *lb, *k, *band, *rebal, *cost*100, *funding*100)
	if *minTurnoverUSD > 0 {
		fmt.Printf("# HISTORICAL universe: min trailing 7-bar avg turnover $%.0f (anti-survivorship)\n", *minTurnoverUSD)
	}
	if *useFundingArch {
		fmt.Printf("# Funding: per-pair historical accrual from funding_archive (%d pairs), scalar fallback %.3f%%/day\n",
			len(cfg.fundingByPairDay), *funding*100)
	}
	if *volWeights || *maxShortWeight > 0 || *volTarget > 0 || *fundingTilt != 0 {
		fmt.Printf("# F1 overlays: vol-weights=%v max-short-weight=%.2f vol-target=%.2f funding-tilt=%.2f\n",
			*volWeights, *maxShortWeight, *volTarget, *fundingTilt)
	}
	if *stopShort > 0 || *stopLong > 0 {
		fmt.Printf("# F2 intra-period stops: stop-short=%.2f stop-long=%.2f (close at daily bar, re-arm each rebalance)\n",
			*stopShort, *stopLong)
	}
	if *tpLeg > 0 || *tpRebal > 0 {
		fmt.Printf("# Profit-taking: tp-leg=%.2f (harvest single winning leg) tp-rebalance=%.2f (triggered early re-run, dynamic cadence)\n",
			*tpLeg, *tpRebal)
	}
	if *trailTP > 0 {
		fmt.Printf("# Trailing take-profit: trail-tp=%.2f (liquidate on retrace below peak, reload next rebalance)\n", *trailTP)
	}
	if *regimeFloor < 1.0 {
		fmt.Printf("# Regime gate: dispersion-median, floor=%.2f window=%d (de-risk in low cross-sectional dispersion)\n", *regimeFloor, *regimeWindow)
	}
	if *makerFillProb < 1.0 {
		fmt.Printf("# F2 maker-fill model: maker-fill-prob=%.2f cost=%.3f%% (fill) taker-cost=%.3f%% (no-fill fallback)\n",
			*makerFillProb, *cost*100, *takerCost*100)
	}
	fmt.Printf("%s\n", dashes(64))
	fmt.Printf("  Final equity:   $%.2f  (start $%.0f)\n", st.eq, *capital)
	fmt.Printf("  Total return:   %+.1f%%\n", st.totRet*100)
	fmt.Printf("  CAGR:           %+.1f%%/yr\n", st.cagr*100)
	fmt.Printf("  Sharpe (ann.):  %.2f\n", st.sharpe)
	fmt.Printf("  Max drawdown:   -%.1f%%\n", st.maxDD*100)
	if st.liquidated {
		fmt.Printf("  Liquidated:     YES (%s)\n", time.Unix(st.liquidatedAt, 0).UTC().Format("2006-01-02"))
	} else {
		fmt.Printf("  Liquidated:     no\n")
	}
	fmt.Printf("  Rebalances:     %d   avg turnover %.0f%%/rebal\n", st.nReb, st.avgTurn*100)
	fmt.Printf("  Funding P&L:    paid $%.2f, received $%.2f, net $%+.2f\n",
		st.fundingPaidUSD, st.fundingRecvUSD, st.fundingRecvUSD-st.fundingPaidUSD)
	fmt.Printf("  Universe:       avg %.1f eligible/rebalance, %d rebalances below 2k=%d (skipped)\n",
		st.avgUniverse, st.nShrunk, 2**k)
	if *stopShort > 0 || *stopLong > 0 {
		avgStop := 0.0
		if st.nStops > 0 {
			avgStop = st.stopRetSum / float64(st.nStops) * 100
		}
		fmt.Printf("  Stops fired:    %d (avg loss at stop %.2f%%)\n", st.nStops, avgStop)
	}
	if *tpLiq > 0 {
		avgTP := 0.0
		if st.nTPLiq > 0 {
			avgTP = st.tpRetSum / float64(st.nTPLiq) * 100
		}
		fmt.Printf("  TP liquidations: %d of %d periods (avg book PnL at liq %+.2f%%)\n",
			st.nTPLiq, st.nReb, avgTP)
	}
	if *tpLeg > 0 {
		avgTPL := 0.0
		if st.nTPLeg > 0 {
			avgTPL = st.tpLegRetSum / float64(st.nTPLeg) * 100
		}
		fmt.Printf("  TP per-leg:     %d legs harvested (avg gain at harvest %+.2f%%)\n", st.nTPLeg, avgTPL)
	}
	if *tpRebal > 0 {
		avgTPR := 0.0
		if st.nTPRebal > 0 {
			avgTPR = st.tpRebalRetSum / float64(st.nTPRebal) * 100
		}
		fmt.Printf("  Early rebalances: %d triggered (avg book PnL at trigger %+.2f%%), avg period %.1f bars (fixed=%d)\n",
			st.nTPRebal, avgTPR, st.avgPeriodBars, *rebal)
	}
	if *trailTP > 0 {
		avgPk := 0.0
		if st.nTrailTP > 0 {
			avgPk = st.trailPeakSum / float64(st.nTrailTP) * 100
		}
		fmt.Printf("  Trailing exits:  %d fired (avg peak PnL at exit %+.2f%%)\n", st.nTrailTP, avgPk)
	}
	if *regimeFloor < 1.0 {
		fmt.Printf("  Regime gate:     de-risked %d of %d rebalances to %.0f%% exposure\n", st.nRegimeGated, st.nReb, *regimeFloor*100)
	}

	// Walk-forward: 4 sub-periods (no CSV output for sub-windows).
	fmt.Printf("\n# WALK-FORWARD (total return %% per sub-period):\n")
	nw := *windows
	if nw < 1 {
		nw = 1
	}
	wfCfg := simConfig{fundingByPairDay: cfg.fundingByPairDay}
	allPos := true
	var wins []simStats
	for w := 0; w < nw; w++ {
		lo := w * len(timeline) / nw
		hi := (w + 1) * len(timeline) / nw
		ws := simulate(data, timeline[lo:hi], years/float64(nw), wfCfg)
		wins = append(wins, ws)
		mark := ""
		if ws.totRet <= 0 {
			allPos = false
			mark = " *NEG"
		}
		if ws.liquidated {
			mark += " *LIQUIDATED"
		}
		stopsNote := ""
		if *stopShort > 0 || *stopLong > 0 {
			stopsNote = fmt.Sprintf(", stops %d", ws.nStops)
		}
		fmt.Printf("  W%d: %+7.1f%%  (Sharpe %.2f, DD -%.0f%%, univ %.0f%s)%s\n",
			w+1, ws.totRet*100, ws.sharpe, ws.maxDD*100, ws.avgUniverse, stopsNote, mark)
	}

	fmt.Printf("\n# VERDICT: ")
	switch verdictFor(st, wins) {
	case "PROMISING":
		fmt.Printf("PROMISING — survives realistic execution AND positive every window.\n#          The first portfolio-level candidate. Next: survivorship-free universe + paper.\n")
	case "MARGINAL":
		fmt.Printf("MARGINAL — net-positive but not stable across all windows (CAGR %+.0f%%, %v all-window+).\n", st.cagr*100, allPos)
	default:
		fmt.Printf("NEGATIVE — the factor does not survive as a real portfolio (CAGR %+.0f%%).\n", st.cagr*100)
	}
}

// loadFundingArchive reads funding_archive(pair, ts, rate) and aggregates rates
// per pair per UTC day (KuCoin funds every 8h → up to 3 rows/day are summed).
// Degrades gracefully (warn + empty map → scalar fallback) if the table is
// missing — another agent may still be building it.
func loadFundingArchive(db *sql.DB) map[string]map[int64]float64 {
	out := map[string]map[int64]float64{}
	rows, err := db.Query("SELECT pair, ts, rate FROM funding_archive")
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN: -funding-archive set but funding_archive unavailable (%v) — falling back to scalar -funding drag\n", err)
		return out
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var pair string
		var ts int64
		var rate float64
		if err := rows.Scan(&pair, &ts, &rate); err != nil {
			continue
		}
		day := ts - ts%86400
		if out[pair] == nil {
			out[pair] = map[int64]float64{}
		}
		out[pair][day] += rate
		n++
	}
	if n == 0 {
		fmt.Fprintln(os.Stderr, "WARN: funding_archive is empty — falling back to scalar -funding drag")
	}
	return out
}

// position is one open leg of the book (for trade CSV + per-pair funding).
type position struct {
	dir      int // +1 long, -1 short
	entryPx  float64
	notional float64
}

// simulate runs the market-neutral momentum portfolio over the timeline slice.
func simulate(data map[string]*pairData, tl []int64, years float64, cfg simConfig) (st simStats) {
	equity := *capital
	peak := equity
	var rets []float64
	var prevLong, prevShort map[string]bool
	openPos := map[string]*position{}
	turnSum := 0.0
	univSum, univN := 0.0, 0

	// F1 vol-target state: EWMA variance of simulated DAILY returns
	// (half-life 12 days → lambda = 0.5^(1/12) per daily observation).
	ewVar := 0.0
	nDaily := 0
	ewLambda := math.Pow(0.5, 1.0/12.0)

	// Dynamic cadence (-tp-rebalance): the outer index advances by the REALIZED
	// period length (i = periodEnd), which equals i+*rebal unless an early
	// rebalance fires. totalPeriodBars accumulates realized holding lengths so
	// Sharpe can be annualized by the true average period. With every dynamic
	// flag off, periodEnd is always i+*rebal → this reproduces the legacy
	// fixed-step loop exactly.
	totalPeriodBars := 0
	var dispHist []float64 // trailing cross-sectional dispersion (past rebalances, for -regime-floor)
	nRegimeGated := 0      // rebalances the regime gate de-risked
	for i := *lb; i+*rebal < len(tl); { // advance is explicit at loop end (i = periodEnd)
		periodEnd := i + *rebal
		tNow, tNext, tPast := tl[i], tl[i+*rebal], tl[i-*lb]
		type pr struct {
			pair     string
			sig, fwd float64
		}
		var prs []pr
		for p, pd := range data {
			cNow, okN := pd.close[tNow]
			cPast, okP := pd.close[tPast]
			cNext, okF := pd.close[tNext]
			if !okN || !okP || !okF || cPast <= 0 || cNow <= 0 {
				continue
			}
			if *minTurnoverUSD > 0 && !eligibleByTurnover(pd, tl, i) {
				continue
			}
			sig := cNow/cPast - 1
			// F1 funding tilt: penalize longs in pairs that cost funding (and
			// vice versa). Uses the rebalance day's summed funding rate from the
			// archive; missing pair-days contribute 0 (no tilt).
			if *fundingTilt != 0 && cfg.fundingByPairDay != nil {
				day := tNow - tNow%86400
				if m, ok := cfg.fundingByPairDay[p]; ok {
					sig -= *fundingTilt * m[day]
				}
			}
			prs = append(prs, pr{pair: p, sig: sig, fwd: cNext/cNow - 1})
		}
		univSum += float64(len(prs))
		univN++
		if len(prs) < 2**k {
			st.nShrunk++
			i = periodEnd // MUST advance before continue — the loop's advance is at
			continue      // the body end now (dynamic cadence), not the for-post.
		}
		scores := make(map[string]float64, len(prs))
		fwdAll := make(map[string]float64, len(prs))
		for _, x := range prs {
			scores[x.pair] = x.sig
			fwdAll[x.pair] = x.fwd
		}

		// (b) Regime gate: cross-sectional dispersion of momentum vs its trailing
		// median. regimeScale de-risks (to -regime-floor) in low-dispersion
		// regimes. Computed from PAST dispersions only; current dispersion is
		// appended AFTER the decision, so there is no look-ahead.
		regimeScale := 1.0
		if *regimeFloor < 1.0 {
			sigVals := make([]float64, len(prs))
			for idx, x := range prs {
				sigVals[idx] = x.sig
			}
			_, disp := meanStd(sigVals)
			if len(dispHist) >= *regimeWindow {
				w := dispHist
				if len(w) > *regimeWindow {
					w = w[len(w)-*regimeWindow:]
				}
				if disp < medianOf(w) {
					regimeScale = *regimeFloor
					nRegimeGated++
				}
			}
			dispHist = append(dispHist, disp)
		}
		// No-trade band (hysteresis): an incumbent name is kept while it stays
		// within the top/bottom (k+band); a fresh name only displaces it once the
		// incumbent leaves that buffer. This cuts churn on names that merely drift
		// across the rank boundary. band=0 reproduces plain top-k/bottom-k exactly.
		// Delegates ranking+selection to internal/xsec.BuildBaskets — the SAME
		// function cmd/xsec-bot and cmd/xsec-live use in production — instead of
		// a private reimplementation, so a backtest can never silently diverge
		// from what the live basket actually does (found 2026-07-23: this used
		// to be a byte-for-byte-identical but separately maintained copy; a diff
		// confirmed zero drift at the time, but nothing enforced that going
		// forward). asOf is unused by this caller (xsec-sim tracks its own
		// per-day timeline), so a zero time.Time is fine here.
		baskets := xsec.BuildBaskets(scores, *k, time.Time{}, *band, prevLong, prevShort)
		longs := baskets.Longs
		shorts := baskets.Shorts
		longSet := map[string]bool{}
		for _, p := range longs {
			longSet[p] = true
		}
		shortSet := map[string]bool{}
		fwdOf := map[string]float64{}
		for _, p := range longs {
			fwdOf[p] = fwdAll[p]
		}
		for _, p := range shorts {
			shortSet[p] = true
			fwdOf[p] = fwdAll[p]
		}

		// F1 per-leg weights: equal (legacy) or inverse-vol; each leg sums to 1
		// (= half the book). Short leg optionally capped per name.
		wLong := legWeights(longs, data, tl, i)
		wShort := legWeights(shorts, data, tl, i)
		if *maxShortWeight > 0 {
			capWeights(wShort, *maxShortWeight)
		}
		// F1 vol targeting: scale gross exposure by clamp(target/realized,
		// 0.25, 1.5), realized = EWMA(half-life 12d) vol of the simulated
		// portfolio's daily returns so far, annualized sqrt(365). Unscaled
		// capital sits in cash (costs and funding scale with exposure too).
		scale := 1.0
		if *volTarget > 0 && nDaily >= 20 && ewVar > 0 {
			realized := math.Sqrt(ewVar * 365.0)
			if realized > 0 {
				scale = *volTarget / realized
				if scale < 0.25 {
					scale = 0.25
				} else if scale > 1.5 {
					scale = 1.5
				}
			}
		}
		scale *= regimeScale // (b) de-risk in low-dispersion regimes (1.0 when gate off)

		// Turnover cost: only positions that rotate in/out pay. Fraction of the
		// 2k-position book that changed × round-trip cost.
		//
		// F2: when -maker-fill-prob < 1.0, each rotated position independently
		// either fills at the maker (post-only) -cost or, with probability
		// (1-maker-fill-prob), fails to fill within the rebalance window and
		// falls back to -taker-cost. A missed fill is modeled as a taker
		// fallback rather than a skipped rebalance: for a market-neutral book
		// BOTH legs must be filled to stay hedged, so leaving a leg unfilled
		// would introduce directional exposure that is far riskier than paying
		// the extra taker spread to guarantee the hedge. The fill outcome is
		// derived deterministically from a hash of (pair, rebalance index) so
		// re-runs are reproducible (no time-seeded RNG).
		var changedPairs []string
		if prevLong != nil {
			for p := range longSet {
				if !prevLong[p] {
					changedPairs = append(changedPairs, p)
				}
			}
			for p := range shortSet {
				if !prevShort[p] {
					changedPairs = append(changedPairs, p)
				}
			}
		} else {
			// Initial entry: every position in the book is a fresh rotation.
			for p := range longSet {
				changedPairs = append(changedPairs, p)
			}
			for p := range shortSet {
				changedPairs = append(changedPairs, p)
			}
		}
		changed := len(changedPairs)
		turnoverFrac := float64(changed) / float64(2**k)
		turnSum += turnoverFrac

		var turnoverCost float64
		if *makerFillProb >= 1.0 {
			// Legacy path: bit-identical to the pre-F2 formula (regression check).
			turnoverCost = turnoverFrac * *cost
		} else {
			perSlot := 1.0 / float64(2**k)
			for _, p := range changedPairs {
				c := *cost
				if !fillsAtMaker(p, i, *makerFillProb) {
					c = *takerCost
				}
				turnoverCost += perSlot * c
			}
		}

		// Trade CSV: closes for positions leaving the book, opens for entrants.
		if cfg.csvTrades != nil {
			date := time.Unix(tNow, 0).UTC().Format("2006-01-02")
			for p, pos := range openPos {
				inBook := (pos.dir > 0 && longSet[p]) || (pos.dir < 0 && shortSet[p])
				if inBook {
					continue
				}
				px := pos.entryPx
				if c, ok := data[p].close[tNow]; ok && c > 0 {
					px = c
				}
				pnl := float64(pos.dir) * (px/pos.entryPx - 1) * pos.notional
				cfg.csvTrades.Write([]string{date, p, sideName(pos.dir), "close",
					fmt.Sprintf("%.8f", px), fmt.Sprintf("%.2f", pos.notional), fmt.Sprintf("%.4f", pnl)})
				delete(openPos, p)
			}
			for p := range longSet {
				if pos, ok := openPos[p]; ok && pos.dir > 0 {
					continue
				}
				notional := equity * scale * wLong[p] / 2.0
				cfg.csvTrades.Write([]string{date, p, "long", "open",
					fmt.Sprintf("%.8f", data[p].close[tNow]), fmt.Sprintf("%.2f", notional), ""})
				openPos[p] = &position{dir: 1, entryPx: data[p].close[tNow], notional: notional}
			}
			for p := range shortSet {
				if pos, ok := openPos[p]; ok && pos.dir < 0 {
					continue
				}
				notional := equity * scale * wShort[p] / 2.0
				cfg.csvTrades.Write([]string{date, p, "short", "open",
					fmt.Sprintf("%.8f", data[p].close[tNow]), fmt.Sprintf("%.2f", notional), ""})
				openPos[p] = &position{dir: -1, entryPx: data[p].close[tNow], notional: notional}
			}
		}

		// DAILY WALK over the holding period (F2). Each position is held at a
		// fixed book fraction w/2 from the period entry (close at tNow) until
		// the rebalance close (tNext) — mathematically identical to the legacy
		// vectorized Σ w*fwd when stops are off — or until an intra-period stop
		// fires: the position is closed at that bar's close, pays the full
		// round-trip -cost on its notional (covering the early exit + eventual
		// re-open; the pair stays in prevLong/prevShort so a re-selection next
		// rebalance is NOT double-charged), realizes its loss, stops accruing
		// funding, and its slice sits in cash until the next rebalance.
		//
		// Funding: with -funding-archive, accrue per pair per day
		// (pnl = -dir * rate_sum_day * fraction; long pays when rate>0, short
		// receives). Pair-days missing from the archive — and the whole walk
		// when the archive is off — fall back to the scalar -funding daily
		// drag, decomposed per-position so the aggregate equals the legacy
		// whole-book drag.
		type leg struct {
			dir     int
			w       float64 // weight within the leg (book fraction = w/2)
			entryPx float64
			open    bool
		}
		book := map[string]*leg{}
		for p, w := range wLong {
			book[p] = &leg{dir: +1, w: w, entryPx: data[p].close[tNow], open: true}
		}
		for p, w := range wShort {
			book[p] = &leg{dir: -1, w: w, entryPx: data[p].close[tNow], open: true}
		}
		gross := 0.0
		stopCostFrac := 0.0
		fundingFrac := 0.0
		earlyEnd := 0        // >0 = bar index where -tp-rebalance fired (shortens the period)
		peakPnL := 0.0       // running peak of book period-PnL fraction (for -trail-tp)
		trailFired := false  // -trail-tp already liquidated this period
		for j := i + 1; j <= i+*rebal; j++ {
			day := tl[j] - tl[j]%86400
			for p, pos := range book {
				if !pos.open {
					continue
				}
				// Funding for day j accrues first (position held through the bar).
				if cfg.fundingByPairDay != nil {
					fundingFrac += pairDayFunding(cfg.fundingByPairDay, p, day, pos.dir) * pos.w / 2.0
				} else {
					fundingFrac += -*funding * pos.w / 2.0
				}
				// Stop check on bars strictly BETWEEN rebalances (the final bar
				// IS the rebalance mark — a stop there would be a no-op + cost).
				if j == i+*rebal {
					continue
				}
				c, ok := data[p].close[tl[j]]
				if !ok || c <= 0 || pos.entryPx <= 0 {
					continue
				}
				ret := float64(pos.dir) * (c/pos.entryPx - 1)
				stopped := (pos.dir < 0 && *stopShort > 0 && c >= pos.entryPx*(1+*stopShort)) ||
					(pos.dir > 0 && *stopLong > 0 && c <= pos.entryPx*(1-*stopLong))
				// Per-leg take-profit: harvest this single leg when its own return
				// reaches +tp-leg. Mutually exclusive with a stop (adverse vs
				// favorable move), so the two never fire on the same bar.
				tpLegHit := *tpLeg > 0 && ret >= *tpLeg
				if !stopped && !tpLegHit {
					continue
				}
				gross += pos.w / 2.0 * ret
				stopCostFrac += pos.w / 2.0 * *cost
				pos.open = false
				action := "stop"
				if tpLegHit {
					action = "tp-leg"
					st.nTPLeg++
					st.tpLegRetSum += ret
				} else {
					st.nStops++
					st.stopRetSum += ret
				}
				if cfg.csvTrades != nil {
					notional := equity * scale * pos.w / 2.0
					cfg.csvTrades.Write([]string{time.Unix(tl[j], 0).UTC().Format("2006-01-02"),
						p, sideName(pos.dir), action, fmt.Sprintf("%.8f", c),
						fmt.Sprintf("%.2f", notional), fmt.Sprintf("%.4f", ret*notional)})
					delete(openPos, p)
				}
			}
			// Portfolio take-profit: liquidate the whole book when its period
			// PnL (realized so far + open mark-to-market at this bar) reaches
			// the threshold. Never on the final bar — that IS the rebalance
			// mark, closing there would be a pure cost no-op.
			if *tpLiq > 0 && j != i+*rebal {
				mtm := gross
				anyOpen := false
				for p, pos := range book {
					if !pos.open || pos.entryPx <= 0 {
						continue
					}
					anyOpen = true
					if c, ok := data[p].close[tl[j]]; ok && c > 0 {
						mtm += pos.w / 2.0 * float64(pos.dir) * (c/pos.entryPx - 1)
					}
				}
				if anyOpen && mtm >= *tpLiq {
					for p, pos := range book {
						if !pos.open || pos.entryPx <= 0 {
							continue
						}
						c, ok := data[p].close[tl[j]]
						if !ok || c <= 0 {
							c = pos.entryPx // no bar: flat close, still pays the exit cost
						}
						gross += pos.w / 2.0 * float64(pos.dir) * (c/pos.entryPx - 1)
						stopCostFrac += pos.w / 2.0 * *cost
						pos.open = false
						if cfg.csvTrades != nil {
							notional := equity * scale * pos.w / 2.0
							cfg.csvTrades.Write([]string{time.Unix(tl[j], 0).UTC().Format("2006-01-02"),
								p, sideName(pos.dir), "tp-liq", fmt.Sprintf("%.8f", c),
								fmt.Sprintf("%.2f", notional), ""})
							delete(openPos, p)
						}
					}
					st.nTPLiq++
					st.tpRetSum += mtm
				}
			}
			// Trailing take-profit: track the book's peak PnL and liquidate on a
			// retracement of -trail-tp points below that (positive) peak. Never on
			// the final bar. Path-dependent: lets a winner run to any height, exits
			// on the reversal, flat until the next rebalance reloads fresh.
			if *trailTP > 0 && !trailFired && j != i+*rebal {
				mtm := gross
				anyOpen := false
				for p, pos := range book {
					if !pos.open || pos.entryPx <= 0 {
						continue
					}
					anyOpen = true
					if c, ok := data[p].close[tl[j]]; ok && c > 0 {
						mtm += pos.w / 2.0 * float64(pos.dir) * (c/pos.entryPx - 1)
					}
				}
				if mtm > peakPnL {
					peakPnL = mtm
				}
				if anyOpen && peakPnL > 0 && peakPnL-mtm >= *trailTP {
					for p, pos := range book {
						if !pos.open || pos.entryPx <= 0 {
							continue
						}
						c, ok := data[p].close[tl[j]]
						if !ok || c <= 0 {
							c = pos.entryPx
						}
						gross += pos.w / 2.0 * float64(pos.dir) * (c/pos.entryPx - 1)
						stopCostFrac += pos.w / 2.0 * *cost
						pos.open = false
						if cfg.csvTrades != nil {
							notional := equity * scale * pos.w / 2.0
							cfg.csvTrades.Write([]string{time.Unix(tl[j], 0).UTC().Format("2006-01-02"),
								p, sideName(pos.dir), "trail-tp", fmt.Sprintf("%.8f", c),
								fmt.Sprintf("%.2f", notional), ""})
							delete(openPos, p)
						}
					}
					trailFired = true
					st.nTrailTP++
					st.trailPeakSum += peakPnL
				}
			}
			// Triggered early rebalance: when the book's period PnL (realized +
			// open mark-to-market at this bar) reaches +tp-rebalance, close the
			// whole book here and END THE PERIOD at this bar so the outer loop
			// re-ranks immediately. Never on the final bar (that IS the scheduled
			// rebalance). The break makes the holding period variable.
			if *tpRebal > 0 && j != i+*rebal {
				mtm := gross
				anyOpen := false
				for p, pos := range book {
					if !pos.open || pos.entryPx <= 0 {
						continue
					}
					anyOpen = true
					if c, ok := data[p].close[tl[j]]; ok && c > 0 {
						mtm += pos.w / 2.0 * float64(pos.dir) * (c/pos.entryPx - 1)
					}
				}
				if anyOpen && mtm >= *tpRebal {
					for p, pos := range book {
						if !pos.open || pos.entryPx <= 0 {
							continue
						}
						c, ok := data[p].close[tl[j]]
						if !ok || c <= 0 {
							c = pos.entryPx // no bar: flat close, still pays the exit cost
						}
						gross += pos.w / 2.0 * float64(pos.dir) * (c/pos.entryPx - 1)
						stopCostFrac += pos.w / 2.0 * *cost
						pos.open = false
						if cfg.csvTrades != nil {
							notional := equity * scale * pos.w / 2.0
							cfg.csvTrades.Write([]string{time.Unix(tl[j], 0).UTC().Format("2006-01-02"),
								p, sideName(pos.dir), "tp-rebal", fmt.Sprintf("%.8f", c),
								fmt.Sprintf("%.2f", notional), ""})
							delete(openPos, p)
						}
					}
					st.nTPRebal++
					st.tpRebalRetSum += mtm
					earlyEnd = j
					break
				}
			}
		}
		if earlyEnd > 0 {
			periodEnd = earlyEnd // period ends early; next rank starts here
		}
		// Settle positions that survived to the rebalance close.
		for p, pos := range book {
			if pos.open {
				gross += pos.w / 2.0 * float64(pos.dir) * fwdOf[p]
			}
		}
		fundingFrac *= scale // funding accrues on deployed notional only
		fundUSD := fundingFrac * equity
		if fundUSD < 0 {
			st.fundingPaidUSD += -fundUSD
		} else {
			st.fundingRecvUSD += fundUSD
		}

		net := scale*(gross-turnoverCost-stopCostFrac) + fundingFrac
		var liq bool
		equity, liq = applyPeriodReturn(equity, net)
		if liq {
			net = -1 // the realized period return is capped at the wiped account
		}
		rets = append(rets, net)

		pb := periodEnd - i // realized holding length in bars (== *rebal unless early)
		totalPeriodBars += pb

		// Feed the EWMA vol estimator with implied daily returns of the
		// realized (post-scale) portfolio return for this holding period.
		if *volTarget > 0 && 1+net > 0 {
			rd := math.Pow(1+net, 1.0/float64(pb)) - 1
			for d := 0; d < pb; d++ {
				if nDaily == 0 {
					ewVar = rd * rd
				} else {
					ewVar = ewLambda*ewVar + (1-ewLambda)*rd*rd
				}
				nDaily++
			}
		}
		if equity > peak {
			peak = equity
		}
		if d := (peak - equity) / peak; d > st.maxDD {
			st.maxDD = d
		}
		prevLong, prevShort = longSet, shortSet
		st.nReb++

		if cfg.csvEquity != nil {
			cfg.csvEquity.Write([]string{
				time.Unix(tl[periodEnd], 0).UTC().Format("2006-01-02"),
				fmt.Sprintf("%.2f", equity),
				fmt.Sprintf("%d", len(longSet)),
				fmt.Sprintf("%d", len(shortSet)),
				fmt.Sprintf("%.2f", equity), // fully deployed: gross = long + short notional = equity
			})
		}
		if liq {
			st.liquidated = true
			st.liquidatedAt = tl[periodEnd]
			break // a liquidated account cannot keep trading
		}
		i = periodEnd // advance by the REALIZED period (dynamic under -tp-rebalance)
	}

	st.eq = equity
	st.totRet = equity/(*capital) - 1
	if years > 0 && equity > 0 {
		st.cagr = math.Pow(equity/(*capital), 1/years) - 1
	} else if equity <= 0 {
		st.cagr = -1
	}
	// Annualize Sharpe by the REALIZED average holding period. With fixed
	// cadence (every dynamic flag off) avgPeriodBars == *rebal exactly, so this
	// reduces to the legacy sqrt(365/rebal). Under -tp-rebalance the period
	// length varies, and annualizing by the true average avoids the inflated
	// Sharpe a naive sqrt(365/rebal) would produce for shorter triggered periods.
	if st.nReb > 0 {
		st.avgPeriodBars = float64(totalPeriodBars) / float64(st.nReb)
	}
	st.nRegimeGated = nRegimeGated
	if len(rets) > 2 {
		m, sd := meanStd(rets)
		annBars := float64(*rebal)
		if st.avgPeriodBars > 0 {
			annBars = st.avgPeriodBars
		}
		if sd > 0 && annBars > 0 {
			st.sharpe = m / sd * math.Sqrt(365.0/annBars)
		}
	}
	if st.nReb > 0 {
		st.avgTurn = turnSum / float64(st.nReb)
	}
	if univN > 0 {
		st.avgUniverse = univSum / float64(univN)
	}
	return
}

// pairDayFunding returns the funding pnl fraction (per unit notional) for one
// pair-day: -dir*rateSum if the archive has the day, else the scalar drag.
func pairDayFunding(arch map[string]map[int64]float64, pair string, day int64, dir int) float64 {
	if m, ok := arch[pair]; ok {
		if rate, ok := m[day]; ok {
			return -float64(dir) * rate
		}
	}
	return -*funding // scalar fallback: always a drag, per position per day
}

// eligibleByTurnover checks the trailing 7-bar avg daily turnover (volume*close)
// at timeline index i. Requires at least 5 of the last 7 bars present.
func eligibleByTurnover(pd *pairData, tl []int64, i int) bool {
	sum, n := 0.0, 0
	for j := i - 6; j <= i; j++ {
		if j < 0 {
			continue
		}
		if tv, ok := pd.turnover[tl[j]]; ok {
			sum += tv
			n++
		}
	}
	if n < 5 {
		return false
	}
	return sum/float64(n) >= *minTurnoverUSD
}

// legWeights returns the weight of each pair within one leg (weights sum to 1).
// Default: equal weight. With -vol-weights: weight_i ∝ 1/sigma_i where sigma is
// the stddev of daily log returns over the last 20 bars before the rebalance
// (timeline indices i-20..i). Pairs with <15 returns get the mean inverse-vol
// of the other leg members (or equal weight if no pair has enough history).
func legWeights(pairs []string, data map[string]*pairData, tl []int64, i int) map[string]float64 {
	w := make(map[string]float64, len(pairs))
	if len(pairs) == 0 {
		return w
	}
	if !*volWeights {
		eq := 1.0 / float64(len(pairs))
		for _, p := range pairs {
			w[p] = eq
		}
		return w
	}
	invVol := make(map[string]float64, len(pairs))
	sumKnown, nKnown := 0.0, 0
	for _, p := range pairs {
		var rets []float64
		prev := 0.0
		for j := i - 20; j <= i; j++ {
			if j < 0 {
				continue
			}
			c, ok := data[p].close[tl[j]]
			if !ok || c <= 0 {
				continue
			}
			if prev > 0 {
				rets = append(rets, math.Log(c/prev))
			}
			prev = c
		}
		if len(rets) >= 15 {
			_, sd := meanStd(rets)
			if sd > 0 {
				invVol[p] = 1.0 / sd
				sumKnown += invVol[p]
				nKnown++
			}
		}
	}
	if nKnown == 0 { // nobody has history → equal weight
		eq := 1.0 / float64(len(pairs))
		for _, p := range pairs {
			w[p] = eq
		}
		return w
	}
	fill := sumKnown / float64(nKnown) // mean inverse-vol for short-history pairs
	total := 0.0
	for _, p := range pairs {
		iv, ok := invVol[p]
		if !ok {
			iv = fill
		}
		w[p] = iv
		total += iv
	}
	for p := range w {
		w[p] /= total
	}
	return w
}

// capWeights caps any single weight at maxW (fraction of the leg), iteratively
// redistributing the excess to uncapped members proportionally. If the cap is
// infeasible (len*maxW < 1) every member ends at maxW and the remainder of the
// leg stays in cash.
func capWeights(w map[string]float64, maxW float64) {
	for iter := 0; iter < len(w)+1; iter++ {
		excess := 0.0
		uncappedSum := 0.0
		for _, v := range w {
			if v > maxW {
				excess += v - maxW
			} else if v < maxW {
				uncappedSum += v
			}
		}
		if excess <= 1e-12 {
			return
		}
		for p, v := range w {
			if v > maxW {
				w[p] = maxW
			}
		}
		if uncappedSum <= 1e-12 {
			return // infeasible: all capped, remainder is cash
		}
		for p, v := range w {
			if v < maxW {
				w[p] = v + excess*v/uncappedSum
			}
		}
	}
}

// fillsAtMaker deterministically decides whether a rotation order for `pair`
// at rebalance step `idx` fills at the maker (post-only) price, given fill
// probability `prob`. Deterministic (hash-based) instead of math/rand so runs
// are reproducible across invocations — required for the regression check
// (-maker-fill-prob 1.0 must reproduce identical results) and for comparing
// sweeps apples-to-apples.
func fillsAtMaker(pair string, idx int, prob float64) bool {
	if prob >= 1.0 {
		return true
	}
	if prob <= 0.0 {
		return false
	}
	h := fnv.New64a()
	fmt.Fprintf(h, "%s|%d", pair, idx)
	u := float64(h.Sum64()%1_000_000) / 1_000_000.0
	return u < prob
}

func sideName(dir int) string {
	if dir > 0 {
		return "long"
	}
	return "short"
}

// medianOf returns the median of xs (does not mutate the caller's slice).
func medianOf(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	c := make([]float64, len(xs))
	copy(c, xs)
	sort.Float64s(c)
	n := len(c)
	if n%2 == 1 {
		return c[n/2]
	}
	return (c[n/2-1] + c[n/2]) / 2
}

func meanStd(x []float64) (float64, float64) {
	n := float64(len(x))
	m := 0.0
	for _, v := range x {
		m += v
	}
	m /= n
	va := 0.0
	for _, v := range x {
		va += (v - m) * (v - m)
	}
	return m, math.Sqrt(va / n)
}
func splitCSVlocal(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
		} else if r != ' ' {
			cur += string(r)
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
func dashes(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = '-'
	}
	return string(b)
}

// fetchContractMultipliers pulls the live KuCoin futures contract list and
// returns pair ("BASE-USDT") -> contract multiplier. Multipliers change
// rarely, so applying today's values across history is an acceptable
// approximation for the turnover-based universe filter.
func fetchContractMultipliers() (map[string]float64, error) {
	cli := &http.Client{Timeout: 15 * time.Second}
	resp, err := cli.Get("https://api-futures.kucoin.com/api/v1/contracts/active")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("contracts/active: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Data []struct {
			Symbol     string  `json:"symbol"`
			Multiplier float64 `json:"multiplier"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	out := make(map[string]float64, len(body.Data))
	for _, c := range body.Data {
		sym := c.Symbol
		if !strings.HasSuffix(sym, "USDTM") {
			continue
		}
		base := strings.TrimSuffix(sym, "USDTM")
		if base == "XBT" {
			base = "BTC"
		}
		out[base+"-USDT"] = c.Multiplier
	}
	return out, nil
}
