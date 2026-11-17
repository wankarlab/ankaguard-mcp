'use client';

// BacktestPanel — form to run cmd/xsec-sim (the validated portfolio backtest
// engine, F4.9) against real historical candle_archive data via the same
// async job runner ActionsPanel uses for rebalance/sync-book. Read-only:
// no exchange calls, no money at risk (see the "backtest" mode gate in
// internal/api/v1/handlers/xsec_jobs_handler.go — not in
// xsecJobModesRequiringLevel2). k/lookback default to the config validated in
// production (see project memory 2026-06-27); capital and the liquidity floor
// prefill from the real account/params so a fresh run reflects this account,
// not an arbitrary number.

import { useEffect, useState } from 'react';
import { useTranslations } from 'next-intl';
import useSWR from 'swr';
import { ApiError } from '@/lib/api';
import { getPortfolioOverview, type PortfolioOverviewLite } from '@/lib/motor-api';
import {
  createXsecJob,
  getXsecJob,
  getXsecParams,
  getXsecSuggestParams,
  type SuggestParams,
  type XSecJob,
  type XSecParam,
  type XSecApiErrorBody,
} from '@/lib/xsec-api';
import { PromoteToLivePanel } from '@/components/xsec/PromoteToLivePanel';

// Research-validated presets (see project memory). Robust = the OOS-selected
// config (2026-07-25); Research = the k10/lb10 basket originally deployed
// (2026-06-27); Conservative = lower k + slower rebalance for smaller books.
const PRESETS: { id: string; lb: string; k: string; rebal: string; band: string; vw: boolean }[] = [
  { id: 'robust', lb: '14', k: '12', rebal: '5', band: '0', vw: true },
  { id: 'research', lb: '10', k: '10', rebal: '7', band: '2', vw: true },
  { id: 'conservative', lb: '14', k: '6', rebal: '10', band: '1', vw: true },
];

const ROBUST_PRESET = PRESETS[0];

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

// Mirrors sweepParamFlag's ranges in internal/api/v1/handlers/xsec_job_runner.go
// — kept in sync manually since the backend is the actual source of truth
// (this is a client-side UX pre-check, the backend re-validates regardless).
const SWEEP_RANGES: Record<'lb' | 'k' | 'rebal' | 'band', [number, number]> = {
  lb: [2, 120], k: [1, 20], rebal: [1, 60], band: [0, 10],
};

// Parses an optional numeric field: blank text = "omitted" (valid, undefined
// value — xsec-sim's own flag default applies); non-blank must parse and lie
// within [min,max], mirroring the backend's own per-field validation.
function parseOptional(text: string, min: number, max: number): { num: number | undefined; valid: boolean } {
  if (text.trim() === '') return { num: undefined, valid: true };
  const n = parseFloat(text);
  return { num: n, valid: isFinite(n) && n >= min && n <= max };
}

const PAIR_RE = /^[A-Z0-9]{1,15}-USDT$/;

function errText(e: unknown, t: ReturnType<typeof useTranslations>): string {
  if (e instanceof ApiError) {
    if (e.status === 403) return `403 · ${t('err_403')}`;
    if (e.status === 401) return `401 · ${t('err_401')}`;
    const body = e.body as XSecApiErrorBody | undefined;
    if (e.status === 409) return `409 · ${body?.error?.message || t('err_conflict')}`;
    const detail = body?.error?.message || '';
    return `${e.status}${detail ? ` · ${detail}` : ''}`;
  }
  return t('action_error');
}

interface Props {
  canWrite: boolean;
  anyRunning: boolean;
  onJobCreated: (jobId: number) => void;
  onConflict: (runningJobId: number | null) => void;
  onAddToCompare?: (jobId: number) => void;
  /** Shared jobs poll + the job the monitor is focused on (drives the promote-to-live section). */
  jobs?: XSecJob[];
  focusJobId?: number | null;
  onJobsChanged?: () => void;
}

export function BacktestPanel({
  canWrite, anyRunning, onJobCreated, onConflict, onAddToCompare,
  jobs = [], focusJobId = null, onJobsChanged,
}: Props) {
  const t = useTranslations('Xsec');

  // Promote-to-live source: ONLY the focused job, and only when it is a
  // finished single backtest (never a sweep, a comparison, or a running/failed
  // job). The list truncates output, so fetch the full job — same SWR key as
  // JobMonitor, so the cache is shared.
  const promoteCandidate = jobs.find(
    (j) => j.id === focusJobId && j.mode === 'backtest' && j.status === 'done',
  );
  const { data: promoteJob } = useSWR<XSecJob>(
    promoteCandidate ? `xsec:job:${promoteCandidate.id}` : null,
    () => getXsecJob(promoteCandidate!.id),
    { revalidateOnFocus: false },
  );

  // Defaults = the research-validated basket config (k10/lb10/band2/rebal7,
  // vol-weights on) — same shape ActionsPanel defaults to for the live basket.
  const [lb, setLb] = useState('10');
  const [k, setK] = useState('10');
  const [rebal, setRebal] = useState('7');
  const [band, setBand] = useState('2');
  const [cost, setCost] = useState('0.0012');
  const [funding, setFunding] = useState('0.0001');
  const [capital, setCapital] = useState('');
  const [capitalTouched, setCapitalTouched] = useState(false);
  const [volWeights, setVolWeights] = useState(true);
  const [minTurnoverUsd, setMinTurnoverUsd] = useState('');
  const [minTurnoverTouched, setMinTurnoverTouched] = useState(false);
  const [windows, setWindows] = useState('4');

  // Assisted parametrization (F4.10): capital + leverage → best feasible config.
  // Prefills the whole form from GET /xsec/suggest-params (universe-aware) so
  // the operator doesn't hand-tune k / per-leg size for a given account.
  const [assistCapital, setAssistCapital] = useState('');
  const [assistLeverage, setAssistLeverage] = useState('1');
  const [assistUtil, setAssistUtil] = useState('0.80');
  const [assistBusy, setAssistBusy] = useState(false);
  const [assist, setAssist] = useState<SuggestParams | null>(null);
  const [assistErr, setAssistErr] = useState<string | null>(null);

  // Sweep (Fase 2): when enabled, ONE of lb/k/rebal/band is driven by a
  // comma-separated list of values instead of its single NumField — the rest
  // of the config stays shared across every run in the sweep.
  const [sweepEnabled, setSweepEnabled] = useState(false);
  const [sweepParam, setSweepParam] = useState<'lb' | 'k' | 'rebal' | 'band'>('k');
  const [sweepValuesText, setSweepValuesText] = useState('8,9,10,11,12');

  // Advanced (Fase 3) — collapsed by default, all optional. Blank = omitted
  // (xsec-sim's own flag default applies), independent of whether the
  // section is currently expanded.
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [stopShort, setStopShort] = useState('');
  const [stopLong, setStopLong] = useState('');
  const [tpLiquidate, setTpLiquidate] = useState('');
  const [tpLeg, setTpLeg] = useState('');
  const [tpRebalance, setTpRebalance] = useState('');
  const [trailTp, setTrailTp] = useState('');
  const [regimeFloor, setRegimeFloor] = useState('');
  const [regimeWindow, setRegimeWindow] = useState('');
  const [makerFillProb, setMakerFillProb] = useState('');
  const [takerCost, setTakerCost] = useState('');
  const [volTarget, setVolTarget] = useState('');
  const [maxShortWeight, setMaxShortWeight] = useState('');
  const [fundingTilt, setFundingTilt] = useState('');
  const [pairs, setPairs] = useState('');
  const [startFrac, setStartFrac] = useState('');
  const [endFrac, setEndFrac] = useState('');

  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  // Prefill capital from the real account equity, unless the user already
  // typed something — same pattern ActionsPanel uses for its capital field.
  const { data: overview } = useSWR<PortfolioOverviewLite>(
    'xsec:portfolio-overview',
    () => getPortfolioOverview(),
    { revalidateOnFocus: false, refreshInterval: 30_000 },
  );
  useEffect(() => {
    if (!capitalTouched && overview?.futures.account_equity !== undefined) {
      setCapital(overview.futures.account_equity.toFixed(2));
    }
  }, [overview, capitalTouched]);

  // Seed the assisted-mode capital from the real account equity once.
  useEffect(() => {
    if (overview?.futures.account_equity !== undefined) {
      setAssistCapital((cur) => (cur === '' ? overview.futures.account_equity!.toFixed(2) : cur));
    }
  }, [overview]);

  // Prefill the liquidity floor from the real xsec_params row this account
  // actually runs with — shares the 'xsec:params' SWR cache with ParamsEditor.
  const { data: params } = useSWR<XSecParam[]>('xsec:params', getXsecParams, {
    revalidateOnFocus: false, refreshInterval: 20_000,
  });
  useEffect(() => {
    if (minTurnoverTouched || !params) return;
    const p = params.find((x) => x.key === 'xsec.min_turnover_usd');
    if (p) setMinTurnoverUsd(p.value);
  }, [params, minTurnoverTouched]);

  const lbNum = parseFloat(lb);
  const kNum = parseFloat(k);
  const rebalNum = parseFloat(rebal);
  const bandNum = parseFloat(band);
  const costNum = parseFloat(cost);
  const fundingNum = parseFloat(funding);
  const capitalNum = parseFloat(capital);
  const minTurnoverNum = parseFloat(minTurnoverUsd);
  const windowsNum = parseFloat(windows);

  // When a field is the swept dimension, its own single-value NumField is
  // hidden (see the JSX below) and its bound check is skipped here — the
  // sweep_values list is validated separately, against the SAME range.
  const swept = (p: 'lb' | 'k' | 'rebal' | 'band') => sweepEnabled && sweepParam === p;
  const sweepValues = sweepValuesText.split(',').map((s) => parseFloat(s.trim())).filter((v) => isFinite(v));
  const sweepRange = SWEEP_RANGES[sweepParam];
  const sweepValuesValid = !sweepEnabled || (
    sweepValues.length >= 1 && sweepValues.length <= 24 &&
    sweepValues.every((v) => v >= sweepRange[0] && v <= sweepRange[1])
  );
  const advStopShort = parseOptional(stopShort, 0, 0.5);
  const advStopLong = parseOptional(stopLong, 0, 0.5);
  const advTpLiquidate = parseOptional(tpLiquidate, 0, 0.2);
  const advTpLeg = parseOptional(tpLeg, 0, 1.0);
  const advTpRebalance = parseOptional(tpRebalance, 0, 0.5);
  const advTrailTp = parseOptional(trailTp, 0, 0.5);
  const advRegimeFloor = parseOptional(regimeFloor, 0, 1);
  const advRegimeWindow = parseOptional(regimeWindow, 2, 60);
  const advMakerFillProb = parseOptional(makerFillProb, 0, 1);
  const advTakerCost = parseOptional(takerCost, 0, 0.02);
  const advVolTarget = parseOptional(volTarget, 0, 3);
  const advMaxShortWeight = parseOptional(maxShortWeight, 0, 1);
  const advFundingTilt = parseOptional(fundingTilt, -1, 1);
  const advStartFrac = parseOptional(startFrac, 0, 1);
  const advEndFrac = parseOptional(endFrac, 0, 1);
  const advFracOrderValid = advStartFrac.num == null || advEndFrac.num == null || advStartFrac.num < advEndFrac.num;
  const pairsList = pairs.split(',').map((p) => p.trim()).filter((p) => p !== '');
  const advPairsValid = pairs.trim() === '' || pairsList.every((p) => PAIR_RE.test(p));

  const inputsValid =
    (swept('lb') || (isFinite(lbNum) && lbNum >= 2 && lbNum <= 120)) &&
    (swept('k') || (isFinite(kNum) && kNum >= 1 && kNum <= 20)) &&
    (swept('rebal') || (isFinite(rebalNum) && rebalNum >= 1 && rebalNum <= 60)) &&
    (swept('band') || (isFinite(bandNum) && bandNum >= 0 && bandNum <= 10)) &&
    isFinite(costNum) && costNum >= 0 && costNum <= 0.02 &&
    isFinite(fundingNum) && fundingNum >= -0.01 && fundingNum <= 0.01 &&
    isFinite(capitalNum) && capitalNum >= 10 && capitalNum <= 1_000_000 &&
    (minTurnoverUsd.trim() === '' || (isFinite(minTurnoverNum) && minTurnoverNum >= 0 && minTurnoverNum <= 50_000_000)) &&
    isFinite(windowsNum) && windowsNum >= 1 && windowsNum <= 12 &&
    sweepValuesValid &&
    advStopShort.valid && advStopLong.valid && advTpLiquidate.valid && advTpLeg.valid && advTpRebalance.valid &&
    advTrailTp.valid && advRegimeFloor.valid && advRegimeWindow.valid && advMakerFillProb.valid &&
    advTakerCost.valid && advVolTarget.valid && advMaxShortWeight.valid && advFundingTilt.valid &&
    advStartFrac.valid && advEndFrac.valid && advFracOrderValid && advPairsValid;

  // Assisted parametrization: ask the backend for the best feasible config for
  // this capital, then prefill every backtest field with it.
  async function runAssist() {
    const cap = parseFloat(assistCapital);
    const lev = parseFloat(assistLeverage);
    const util = parseFloat(assistUtil);
    if (!isFinite(cap) || cap < 10 || cap > 10_000_000 ||
        !isFinite(lev) || lev < 1 || lev > 20 ||
        !isFinite(util) || util <= 0 || util > 1) {
      setAssistErr(t('assist_input_invalid'));
      return;
    }
    setAssistBusy(true);
    setAssistErr(null);
    try {
      const r = await getXsecSuggestParams(cap, lev, util);
      setAssist(r);
      // Prefill the form so the operator can review, run, or tweak.
      setLb(String(r.lookback));
      setK(String(r.k));
      setRebal(String(r.rebal));
      setBand(String(r.band));
      setVolWeights(r.vol_weights);
      setCapitalTouched(true);
      setCapital(r.capital.toFixed(2));
      if (sweepEnabled) setSweepEnabled(false); // single-config recommendation
    } catch (e) {
      setAssistErr(errText(e, t));
    } finally {
      setAssistBusy(false);
    }
  }

  function warnLabel(code: string): string {
    switch (code) {
      case 'universe_unavailable': return t('warn_universe_unavailable');
      case 'k_capped_by_universe': return t('warn_k_capped_by_universe');
      case 'k_reduced_for_min_leg': return t('warn_k_reduced_for_min_leg');
      case 'capital_below_min': return t('warn_capital_below_min');
      case 'high_leverage': return t('warn_high_leverage');
      default: return code;
    }
  }

  // buildRunBase assembles the shared job body from the current form state.
  // Reused by run() and runCompareRobust() so both send an identical shape.
  function buildRunBase() {
    return {
      lookback: swept('lb') ? undefined : lbNum,
      k: swept('k') ? undefined : kNum,
      rebal: swept('rebal') ? undefined : rebalNum,
      band: swept('band') ? undefined : bandNum,
      cost: costNum,
      funding: fundingNum,
      capital: capitalNum,
      vol_weights: volWeights,
      min_turnover_usd: minTurnoverUsd.trim() === '' ? undefined : minTurnoverNum,
      windows: windowsNum,
      stop_short: advStopShort.num,
      stop_long: advStopLong.num,
      tp_liquidate: advTpLiquidate.num,
      tp_leg: advTpLeg.num,
      tp_rebalance: advTpRebalance.num,
      trail_tp: advTrailTp.num,
      regime_floor: advRegimeFloor.num,
      regime_window: advRegimeWindow.num,
      maker_fill_prob: advMakerFillProb.num,
      taker_cost: advTakerCost.num,
      vol_target: advVolTarget.num,
      max_short_weight: advMaxShortWeight.num,
      funding_tilt: advFundingTilt.num,
      pairs: pairsList.length > 0 ? pairsList.join(',') : undefined,
      start_frac: advStartFrac.num,
      end_frac: advEndFrac.num,
    };
  }

  async function run() {
    if (!canWrite || busy || !inputsValid) return;
    setBusy(true);
    setMsg(null);
    try {
      const base = buildRunBase();
      const res = sweepEnabled
        ? await createXsecJob({ ...base, mode: 'backtest-sweep', sweep_param: sweepParam, sweep_values: sweepValues })
        : await createXsecJob({ ...base, mode: 'backtest' });
      setMsg({ kind: 'ok', text: t(sweepEnabled ? 'msg_sweep_created' : 'msg_backtest_created', { id: res.job_id }) });
      onJobCreated(res.job_id);
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        const body = e.body as XSecApiErrorBody | undefined;
        onConflict(body?.running_job_id ?? null);
      }
      setMsg({ kind: 'err', text: errText(e, t) });
    } finally {
      setBusy(false);
    }
  }

  function applyPreset(p: (typeof PRESETS)[number]) {
    if (sweepEnabled) setSweepEnabled(false);
    setLb(p.lb); setK(p.k); setRebal(p.rebal); setBand(p.band); setVolWeights(p.vw);
  }

  // Run the current config, then (single-flight forces sequential) the robust
  // OOS config with the SAME econ params, and add both jobs to the compare
  // panel so their equity curves overlay. Advanced risk overlays are stripped
  // from the robust comparand so it's the clean reference config.
  async function runCompareRobust() {
    if (!canWrite || busy || !inputsValid || sweepEnabled) return;
    setBusy(true);
    setMsg(null);
    try {
      const a = await createXsecJob({ ...buildRunBase(), mode: 'backtest' });
      onJobCreated(a.job_id);
      onAddToCompare?.(a.job_id);
      setMsg({ kind: 'ok', text: t('msg_compare_wait', { id: a.job_id }) });

      // Wait for A to finish (backtests run one at a time).
      for (let i = 0; i < 300; i++) {
        const j = await getXsecJob(a.job_id);
        if (j.status !== 'running') break;
        await sleep(1000);
      }

      const robustBase = {
        ...buildRunBase(),
        lookback: parseInt(ROBUST_PRESET.lb, 10),
        k: parseInt(ROBUST_PRESET.k, 10),
        rebal: parseInt(ROBUST_PRESET.rebal, 10),
        band: parseInt(ROBUST_PRESET.band, 10),
        vol_weights: ROBUST_PRESET.vw,
        stop_short: undefined, stop_long: undefined, tp_liquidate: undefined,
        tp_leg: undefined, tp_rebalance: undefined, trail_tp: undefined,
        regime_floor: undefined, regime_window: undefined,
      };
      const b = await createXsecJob({ ...robustBase, mode: 'backtest' });
      onJobCreated(b.job_id);
      onAddToCompare?.(b.job_id);
      setMsg({ kind: 'ok', text: t('msg_compare_robust_done', { a: a.job_id, b: b.job_id }) });
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        const body = e.body as XSecApiErrorBody | undefined;
        onConflict(body?.running_job_id ?? null);
      }
      setMsg({ kind: 'err', text: errText(e, t) });
    } finally {
      setBusy(false);
    }
  }

  // Live sizing preview for the assisted card (client-side, no network): the
  // derived gross/margin update as you type, before pressing Suggest.
  const assistCapNum = parseFloat(assistCapital);
  const assistLevNum = parseFloat(assistLeverage);
  const assistUtilNum = parseFloat(assistUtil);
  const assistPreviewValid =
    isFinite(assistCapNum) && assistCapNum > 0 &&
    isFinite(assistLevNum) && assistLevNum > 0 &&
    isFinite(assistUtilNum) && assistUtilNum > 0 && assistUtilNum <= 1;
  const assistGross = assistPreviewValid ? assistCapNum * assistLevNum * assistUtilNum : NaN;
  const assistMargin = assistPreviewValid ? assistCapNum * assistUtilNum : NaN;

  const fieldFor: Record<'lb' | 'k' | 'rebal' | 'band', { label: string; value: string; onChange: (v: string) => void }> = {
    lb: { label: t('field_lb'), value: lb, onChange: setLb },
    k: { label: t('field_k'), value: k, onChange: setK },
    rebal: { label: t('field_rebal'), value: rebal, onChange: setRebal },
    band: { label: t('field_band'), value: band, onChange: setBand },
  };

  // Single source of truth per button: used for BOTH `disabled` and the style.
  const assistDisabled = !canWrite || assistBusy;
  const runDisabled = !canWrite || anyRunning || busy || !inputsValid;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {/* Assisted parametrization — capital + leverage → best feasible config. */}
      <div style={{ padding: 12, borderRadius: 'var(--r)', border: '1px solid var(--brand)', background: 'rgba(108,140,255,.04)' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
          <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--tx-hi)' }}>{t('assist_title')}</span>
          <span className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)' }}>{t('assist_subtitle')}</span>
        </div>
        <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', alignItems: 'flex-end' }}>
          <NumField label={t('assist_capital')} value={assistCapital} onChange={setAssistCapital} disabled={!canWrite} />
          <NumField label={t('assist_leverage')} value={assistLeverage} onChange={setAssistLeverage} disabled={!canWrite} />
          <NumField label={t('assist_util')} value={assistUtil} onChange={setAssistUtil} disabled={!canWrite} />
          <button
            type="button"
            disabled={assistDisabled} suppressHydrationWarning
            onClick={() => void runAssist()}
            style={btnStyle(assistDisabled)}
          >
            {assistBusy ? t('working') : t('assist_button')}
          </button>
        </div>
        <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', marginTop: 6 }}>{t('assist_leverage_sim_hint')}</div>
        {assistPreviewValid && (
          <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', marginTop: 6 }}>
            {t('assist_preview', { gross: usd(assistGross), margin: usd(assistMargin) })}
          </div>
        )}
        {assistErr && (
          <div className="mono" style={{ fontSize: 11, color: 'var(--down)', marginTop: 8 }}>{assistErr}</div>
        )}
        {assist && (
          <div style={{ marginTop: 10, display: 'flex', flexDirection: 'column', gap: 8 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <span
                className="mono"
                style={{
                  fontSize: 10, padding: '2px 8px', borderRadius: 4, fontWeight: 600,
                  color: assist.feasible ? 'var(--up)' : 'var(--down)',
                  background: assist.feasible ? 'rgba(74,219,132,.1)' : 'rgba(233,90,90,.1)',
                }}
              >
                {assist.feasible ? t('assist_feasible') : t('assist_infeasible')}
              </span>
              <span className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)' }}>
                {assist.universe_size >= 0
                  ? t('assist_universe', { n: assist.universe_size })
                  : t('assist_universe_unknown')}
              </span>
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(120px, 1fr))', gap: 8 }}>
              <AssistMetric label={t('assist_m_config')} value={`k=${assist.k} · lb${assist.lookback} · rebal${assist.rebal} · band${assist.band}`} />
              <AssistMetric label={t('assist_m_legs')} value={`${assist.legs} (${assist.k}×2)`} />
              <AssistMetric label={t('assist_m_per_leg')} value={usd(assist.per_leg_usd)} />
              <AssistMetric label={t('assist_m_gross')} value={usd(assist.gross_exposure)} />
              <AssistMetric label={t('assist_m_margin')} value={usd(assist.margin_used)} />
              <AssistMetric label={t('assist_m_max_total')} value={usd(assist.max_total_usd)} />
            </div>
            {assist.warnings.length > 0 && (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
                {assist.warnings.map((w) => (
                  <div key={w} className="mono" style={{ fontSize: 10, color: 'var(--tx-dim)' }}>· {warnLabel(w)}</div>
                ))}
              </div>
            )}
            <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)' }}>
              {t('assist_live_hint', { pos: usd(assist.max_pos_usd), total: usd(assist.max_total_usd) })}
            </div>
          </div>
        )}
      </div>

      {/* One-click presets — load a research-validated config into the form. */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
        <span className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', textTransform: 'uppercase' }}>{t('presets_label')}</span>
        {PRESETS.map((p) => (
          <button
            key={p.id}
            type="button"
            disabled={!canWrite} suppressHydrationWarning
            onClick={() => applyPreset(p)}
            title={t(`preset_${p.id}_desc`)}
            style={{
              padding: '4px 10px', borderRadius: 999, border: '1px solid var(--line)',
              background: 'var(--bg-void)', color: canWrite ? 'var(--tx-hi)' : 'var(--tx-faint)',
              fontSize: 11, fontFamily: 'var(--mono)', cursor: canWrite ? 'pointer' : 'not-allowed',
            }}
          >
            {t(`preset_${p.id}`)}
          </button>
        ))}
      </div>

      <div style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '8px 12px', borderRadius: 'var(--r)', border: '1px solid var(--line)' }}>
        <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12, color: 'var(--tx-dim)' }}>
          <input type="checkbox" checked={sweepEnabled} onChange={(e) => setSweepEnabled(e.target.checked)} disabled={!canWrite} suppressHydrationWarning />
          {t('field_sweep_enable')}
        </label>
        {sweepEnabled && (
          <>
            <select
              value={sweepParam}
              onChange={(e) => setSweepParam(e.target.value as typeof sweepParam)}
              disabled={!canWrite} suppressHydrationWarning
              style={{ ...inputStyle, width: 90 }}
            >
              <option value="lb">{t('field_lb')}</option>
              <option value="k">{t('field_k')}</option>
              <option value="rebal">{t('field_rebal')}</option>
              <option value="band">{t('field_band')}</option>
            </select>
            <input
              type="text"
              value={sweepValuesText}
              onChange={(e) => setSweepValuesText(e.target.value)}
              disabled={!canWrite} suppressHydrationWarning
              placeholder="8,9,10,11,12"
              style={{ ...inputStyle, width: 200 }}
            />
            <span className="mono" style={{ fontSize: 10, color: sweepValuesValid ? 'var(--tx-faint)' : 'var(--down)' }}>
              {t('sweep_values_hint', { count: sweepValues.length, min: sweepRange[0], max: sweepRange[1] })}
            </span>
          </>
        )}
      </div>

      <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', alignItems: 'flex-end' }}>
        {(['lb', 'k', 'rebal', 'band'] as const).map((p) => (
          swept(p)
            ? null
            : <NumField key={p} label={fieldFor[p].label} value={fieldFor[p].value} onChange={fieldFor[p].onChange} disabled={!canWrite} />
        ))}
        <NumField label={t('field_windows')} value={windows} onChange={setWindows} disabled={!canWrite} />
      </div>

      <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', alignItems: 'flex-end' }}>
        <NumField label={t('field_cost')} value={cost} onChange={setCost} disabled={!canWrite} />
        <NumField label={t('field_funding')} value={funding} onChange={setFunding} disabled={!canWrite} />
        <NumField
          label={t('field_capital')}
          value={capital}
          onChange={(v) => { setCapitalTouched(true); setCapital(v); }}
          disabled={!canWrite}
        />
        <NumField
          label={t('field_min_turnover_usd')}
          value={minTurnoverUsd}
          onChange={(v) => { setMinTurnoverTouched(true); setMinTurnoverUsd(v); }}
          disabled={!canWrite}
        />
        <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12, color: 'var(--tx-dim)' }}>
          <input type="checkbox" checked={volWeights} onChange={(e) => setVolWeights(e.target.checked)} disabled={!canWrite} suppressHydrationWarning />
          {t('field_vol_weights')}
        </label>
      </div>

      <div>
        <button
          type="button"
          onClick={() => setAdvancedOpen((v) => !v)}
          style={{ background: 'none', border: 'none', padding: 0, cursor: 'pointer', color: 'var(--tx-dim)', fontSize: 11, fontFamily: 'var(--mono)' }}
        >
          {advancedOpen ? '▾' : '▸'} {t('advanced_section_label')}
        </button>
        {advancedOpen && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12, marginTop: 10, padding: 12, borderRadius: 'var(--r)', border: '1px solid var(--line)' }}>
            <div>
              <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', textTransform: 'uppercase', marginBottom: 6 }}>{t('advanced_group_execution')}</div>
              <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', alignItems: 'flex-end' }}>
                <NumField label={t('field_maker_fill_prob')} value={makerFillProb} onChange={setMakerFillProb} disabled={!canWrite} />
                <NumField label={t('field_taker_cost')} value={takerCost} onChange={setTakerCost} disabled={!canWrite} />
              </div>
            </div>
            <div>
              <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', textTransform: 'uppercase', marginBottom: 6 }}>{t('advanced_group_risk')}</div>
              <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', alignItems: 'flex-end' }}>
                <NumField label={t('field_stop_short')} value={stopShort} onChange={setStopShort} disabled={!canWrite} />
                <NumField label={t('field_stop_long')} value={stopLong} onChange={setStopLong} disabled={!canWrite} />
                <NumField label={t('field_tp_liquidate')} value={tpLiquidate} onChange={setTpLiquidate} disabled={!canWrite} />
                <NumField label={t('field_tp_leg')} value={tpLeg} onChange={setTpLeg} disabled={!canWrite} />
                <NumField label={t('field_tp_rebalance')} value={tpRebalance} onChange={setTpRebalance} disabled={!canWrite} />
                <NumField label={t('field_trail_tp')} value={trailTp} onChange={setTrailTp} disabled={!canWrite} />
                <NumField label={t('field_regime_floor')} value={regimeFloor} onChange={setRegimeFloor} disabled={!canWrite} />
                <NumField label={t('field_regime_window')} value={regimeWindow} onChange={setRegimeWindow} disabled={!canWrite} />
              </div>
            </div>
            <div>
              <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', textTransform: 'uppercase', marginBottom: 6 }}>{t('advanced_group_universe')}</div>
              <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', alignItems: 'flex-end' }}>
                <NumField label={t('field_vol_target')} value={volTarget} onChange={setVolTarget} disabled={!canWrite} />
                <NumField label={t('field_max_short_weight')} value={maxShortWeight} onChange={setMaxShortWeight} disabled={!canWrite} />
                <NumField label={t('field_funding_tilt')} value={fundingTilt} onChange={setFundingTilt} disabled={!canWrite} />
                <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 11, color: 'var(--tx-dim)' }}>
                  {t('field_pairs')}
                  <input
                    type="text"
                    value={pairs}
                    onChange={(e) => setPairs(e.target.value)}
                    disabled={!canWrite} suppressHydrationWarning
                    placeholder="BTC-USDT,ETH-USDT"
                    style={{ ...inputStyle, width: 220 }}
                  />
                </label>
              </div>
              {!advPairsValid && (
                <div className="mono" style={{ fontSize: 10, color: 'var(--down)', marginTop: 4 }}>{t('field_pairs_invalid')}</div>
              )}
            </div>
            <div>
              <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', textTransform: 'uppercase', marginBottom: 6 }}>{t('advanced_group_oos')}</div>
              <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', alignItems: 'flex-end' }}>
                <NumField label={t('field_start_frac')} value={startFrac} onChange={setStartFrac} disabled={!canWrite} />
                <NumField label={t('field_end_frac')} value={endFrac} onChange={setEndFrac} disabled={!canWrite} />
              </div>
              {!advFracOrderValid && (
                <div className="mono" style={{ fontSize: 10, color: 'var(--down)', marginTop: 4 }}>{t('field_frac_order_invalid')}</div>
              )}
            </div>
          </div>
        )}
      </div>

      <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap' }}>
        <button
          type="button"
          disabled={runDisabled} suppressHydrationWarning
          onClick={() => void run()}
          style={btnStyle(runDisabled)}
        >
          {busy ? t('working') : sweepEnabled ? t('btn_sweep') : t('btn_backtest')}
        </button>
        {!sweepEnabled && (
          <button
            type="button"
            disabled={runDisabled} suppressHydrationWarning
            onClick={() => void runCompareRobust()}
            title={t('btn_compare_robust_desc')}
            style={{
              padding: '9px 18px', borderRadius: 'var(--r)', border: '1px solid var(--line)',
              background: 'var(--bg-void)',
              color: runDisabled ? 'var(--tx-faint)' : 'var(--tx-hi)',
              fontSize: 13, fontWeight: 600, whiteSpace: 'nowrap',
              cursor: runDisabled ? 'not-allowed' : 'pointer',
            }}
          >
            {t('btn_compare_robust')}
          </button>
        )}
      </div>

      {msg && (
        <div
          className="mono"
          style={{
            fontSize: 11, padding: '8px 12px', borderRadius: 'var(--r)',
            color: msg.kind === 'ok' ? 'var(--up)' : 'var(--down)',
            background: msg.kind === 'ok' ? 'rgba(74,219,132,.07)' : 'rgba(233,90,90,.07)',
          }}
        >
          {msg.text}
        </div>
      )}

      {anyRunning && (
        <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{t('another_job_running')}</div>
      )}

      {/* Promote to live — single finished backtest only (never sweep/compare). */}
      {promoteJob && promoteJob.mode === 'backtest' && promoteJob.status === 'done' && (
        <PromoteToLivePanel
          job={promoteJob}
          canWrite={canWrite}
          anyRunning={anyRunning}
          accountEquity={overview?.futures.account_equity}
          onJobsChanged={() => onJobsChanged?.()}
          onConflict={onConflict}
        />
      )}
    </div>
  );
}

function NumField({ label, value, onChange, disabled }: { label: string; value: string; onChange: (v: string) => void; disabled: boolean }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 11, color: 'var(--tx-dim)' }}>
      {label}
      <input
        type="number"
        value={value}
        disabled={disabled} suppressHydrationWarning
        onChange={(e) => onChange(e.target.value)}
        style={{ ...inputStyle, width: 120 }}
      />
    </label>
  );
}

function AssistMetric({ label, value }: { label: string; value: string }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
      <span className="mono" style={{ fontSize: 9, color: 'var(--tx-faint)', textTransform: 'uppercase' }}>{label}</span>
      <span className="mono" style={{ fontSize: 12, color: 'var(--tx-hi)', fontWeight: 600 }}>{value}</span>
    </div>
  );
}

function usd(n: number): string {
  return `$${n.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

const inputStyle: React.CSSProperties = {
  padding: '7px 10px', borderRadius: 4, border: '1px solid var(--line)',
  background: 'var(--bg-void)', color: 'var(--tx-hi)', fontSize: 12,
};

function btnStyle(disabled: boolean): React.CSSProperties {
  return {
    padding: '9px 18px', borderRadius: 'var(--r)', border: 'none',
    background: disabled ? 'var(--bg-void)' : 'var(--brand)',
    color: disabled ? 'var(--tx-faint)' : 'var(--bg)',
    fontSize: 13, fontWeight: 600, cursor: disabled ? 'not-allowed' : 'pointer', whiteSpace: 'nowrap',
  };
}
