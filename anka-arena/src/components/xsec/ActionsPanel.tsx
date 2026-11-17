'use client';

// ActionsPanel — the four XSec job-creating actions:
//   - Sync Book        (safe, one click)
//   - Rebalance (Dry)  (k + max_pos_usd inputs, always allowed)
//   - Rebalance LIVE   (danger; gated client-side on a matching recent dry-run,
//                        the server re-checks the same rule and 412s otherwise)
//   - Cancel Order     (order id input)
//
// Dry-run-first gating: the same k/max_pos_usd/lookback/max_total_usd inputs
// are shared by the dry-run and LIVE actions. LIVE stays disabled until `jobs`
// (polled by the parent) contains a mode=rebalance-dry job with status=done,
// finished_at within the last 10 minutes, and args matching ALL current
// override inputs (see parseJobArgs in xsec-api.ts for the wire-shape
// assumption on `args`) — mirroring the server's exact-argv 412 gate.

import { useEffect, useMemo, useState } from 'react';
import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { ApiError } from '@/lib/api';
import { findNonBasketPositions, isKillSwitchActiveError } from '@/lib/killswitch-ring0';
import { getPortfolioOverview, getXSecStatus, type PortfolioOverviewLite } from '@/lib/motor-api';
import {
  createXsecJob,
  getXsecJob,
  getXsecSuggestParams,
  parseAutoconfigResult,
  parseRebalanceResult,
  type SuggestParams,
  type XSecJob,
  type XSecApiErrorBody,
} from '@/lib/xsec-api';
import { findMatchingDryRun, isLegBelowMin, XSEC_MIN_LEG_USD } from '@/lib/xsec-live-gate';
import { AutoconfigCard } from '@/components/xsec/AutoconfigCard';
import { RebalancePreview } from '@/components/xsec/RebalancePreview';
import { RebalanceResultReport } from '@/components/xsec/RebalanceResultReport';

// Backend error bodies are models.APIResponse: `error` is an object
// ({code,message}), not a bare string — see XSecApiErrorBody in xsec-api.ts.
function errText(e: unknown, t: ReturnType<typeof useTranslations>): string {
  if (isKillSwitchActiveError(e)) return `423 · ${t('err_killswitch')}`;
  if (e instanceof ApiError) {
    if (e.status === 403) return `403 · ${t('err_403')}`;
    if (e.status === 401) return `401 · ${t('err_401')}`;
    if (e.status === 412) return `412 · ${t('err_412')}`;
    const body = e.body as XSecApiErrorBody | undefined;
    if (e.status === 409) {
      return `409 · ${body?.error?.message || t('err_conflict')}`;
    }
    const detail = body?.error?.message || '';
    return `${e.status}${detail ? ` · ${detail}` : ''}`;
  }
  return t('action_error');
}

interface Props {
  canWrite: boolean;
  jobs: XSecJob[];
  anyRunning: boolean;
  onJobCreated: (jobId: number) => void;
  onConflict: (runningJobId: number | null) => void;
}

export function ActionsPanel({ canWrite, jobs, anyRunning, onJobCreated, onConflict }: Props) {
  const t = useTranslations('Xsec');

  // Defaults = the research-validated basket config actually launched on
  // 2026-07-16 (k10/lb10 vol-weights, $2.50/leg, $50 total) — NOT the
  // xsec-live binary's own flag defaults (k7/lb14/$5/$100), which don't fit
  // this account and reproduce the k-too-low decay of the 07-09 basket.
  const [k, setK] = useState('10');
  const [maxPosUsd, setMaxPosUsd] = useState('2.5');
  const [lookback, setLookback] = useState('10');
  const [maxTotalUsd, setMaxTotalUsd] = useState('50');
  const [orderId, setOrderId] = useState('');
  const [busy, setBusy] = useState<string | null>(null);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  const [confirmLive, setConfirmLive] = useState(false);
  // Danger zone: liquidate-all needs one arm+confirm; relaunch needs the
  // operator to type REINICIAR (both open real-money actions).
  const [confirmCloseAll, setConfirmCloseAll] = useState(false);
  const [relaunchConfirmText, setRelaunchConfirmText] = useState('');
  const relaunchArmed = relaunchConfirmText.trim().toUpperCase() === 'REINICIAR';
  // Whether the structured preview (post a fresh matching dry-run) is shown.
  // Set true right after creating a preview job; reset whenever the override
  // inputs change so a stale preview never lingers on screen.
  const [previewOpen, setPreviewOpen] = useState(false);

  // Dynamic capital distribution: capital / (2*k) per leg, capital as the
  // total cap — auto-fills maxPosUsd/maxTotalUsd from capital+k while
  // autoDistribute is true. Editing maxPosUsd/maxTotalUsd by hand turns it
  // off (see their onChange below) so manual overrides always win.
  const [capital, setCapital] = useState('');
  const [capitalTouched, setCapitalTouched] = useState(false);
  const [autoDistribute, setAutoDistribute] = useState(true);

  // Assisted sizing (F4.10): leverage + utilization → universe-aware, min-
  // contract-aware feasible config from GET /xsec/suggest-params. Replaces the
  // naive capital/(2k) auto-distribute (which can't see the universe cap or the
  // per-pair min contract, the exact reason the 2026-07-26 relaunch entered
  // 12/20 legs). assistBusy doubles as the flag for the "resize & complete" run.
  // The live executor (cmd/xsec-live) always trades at 1x — not editable.
  const LIVE_LEVERAGE = 1;
  const [assistUtil, setAssistUtil] = useState('0.80');
  const [assistBusy, setAssistBusy] = useState(false);
  const [assist, setAssist] = useState<SuggestParams | null>(null);
  const [assistErr, setAssistErr] = useState<string | null>(null);
  // Autoconfig: read-only job that sizes against the REAL equity at 1x.
  const [autoconfigJobId, setAutoconfigJobId] = useState<number | null>(null);
  const [autoconfigBusy, setAutoconfigBusy] = useState(false);
  const [autoconfigErr, setAutoconfigErr] = useState<string | null>(null);
  const [autoconfigApplied, setAutoconfigApplied] = useState<number | null>(null);

  const kNum = parseFloat(k);
  const maxPosNum = parseFloat(maxPosUsd);
  const lookbackNum = parseFloat(lookback);
  const maxTotalNum = parseFloat(maxTotalUsd);
  const inputsValid =
    isFinite(kNum) && kNum > 0 &&
    isFinite(maxPosNum) && maxPosNum > 0 &&
    isFinite(lookbackNum) && lookbackNum > 0 &&
    isFinite(maxTotalNum) && maxTotalNum > 0;

  // Legs below the minimum size get dropped by the exchange (min contract) and
  // leave the basket imbalanced: block every action that opens legs.
  const legTooSmall = inputsValid && isLegBelowMin(maxPosNum);

  // Futures margin context so the basket can be sized against the real
  // account before hitting LIVE. Failure is non-fatal — the strip just hides.
  const { data: overview } = useSWR<PortfolioOverviewLite>(
    'xsec:portfolio-overview',
    () => getPortfolioOverview(),
    { revalidateOnFocus: false, refreshInterval: 30_000 },
  );
  // Real futures positions NOT in the basket registry: "Liquidar todo" does not
  // touch them (only the global kill switch does). Needs the basket legs, so
  // it stays silent until both requests have succeeded.
  const { data: xsecStatus } = useSWR('xsec:status-actions', () => getXSecStatus(), {
    revalidateOnFocus: false, refreshInterval: 30_000,
  });
  const nonBasketPositions = useMemo(
    () => (overview && xsecStatus
      ? findNonBasketPositions(overview.positions, (xsecStatus.positions ?? []).map((p) => p.pair))
      : []),
    [overview, xsecStatus],
  );
  const futuresEquity = overview?.futures.account_equity;
  const availableMargin = overview?.futures.available_balance;
  // Per-side heuristic: one side's notional must be marginable on its own at
  // 1x. Cross-margin nets long vs short, so this is an advisory, not a block.
  const marginWarning =
    inputsValid && futuresEquity !== undefined && kNum * maxPosNum > futuresEquity;

  // Prefill capital from the real available margin as soon as it loads,
  // unless the user already typed something into the field.
  useEffect(() => {
    if (!capitalTouched && availableMargin !== undefined) {
      setCapital(availableMargin.toFixed(2));
    }
  }, [availableMargin, capitalTouched]);

  const capitalNum = parseFloat(capital);
  const capitalValid = isFinite(capitalNum) && capitalNum > 0;

  // Auto-distribute: capital split evenly across 2*k legs (market-neutral —
  // k longs + k shorts). Runs only while autoDistribute is true; manual edits
  // to maxPosUsd/maxTotalUsd (below) turn it off so they always win.
  useEffect(() => {
    if (!autoDistribute || !capitalValid || !isFinite(kNum) || kNum <= 0) return;
    const perLeg = capitalNum / (2 * kNum);
    setMaxPosUsd(perLeg.toFixed(2));
    setMaxTotalUsd(capitalNum.toFixed(2));
  }, [autoDistribute, capitalNum, capitalValid, kNum]);

  function setMaxPosUsdManual(v: string) {
    setAutoDistribute(false);
    setMaxPosUsd(v);
  }
  function setMaxTotalUsdManual(v: string) {
    setAutoDistribute(false);
    setMaxTotalUsd(v);
  }

  const matchingDryRun = useMemo(
    () => (inputsValid ? findMatchingDryRun(jobs, kNum, maxPosNum, lookbackNum, maxTotalNum) : null),
    [jobs, kNum, maxPosNum, lookbackNum, maxTotalNum, inputsValid],
  );
  const liveEnabled = canWrite && !anyRunning && inputsValid && !legTooSmall && matchingDryRun != null;

  // Latest settled live deploy (rebalance-live / relaunch) — its full output
  // drives the failure-analysis + "resize & complete" section. The jobs LIST
  // truncates output to 4KB, so refetch the single job in full for the parse.
  const lastDeployId = useMemo(() => {
    const j = jobs.find((x) => (x.mode === 'rebalance-live' || x.mode === 'relaunch') && x.status === 'done');
    return j?.id ?? null;
  }, [jobs]);
  const { data: lastDeployJob } = useSWR<XSecJob>(
    lastDeployId != null ? `xsec:actionsdeploy:${lastDeployId}` : null,
    () => getXsecJob(lastDeployId as number),
    { revalidateOnFocus: false },
  );
  const deployResult = lastDeployJob ? parseRebalanceResult(lastDeployJob.output) : null;

  // A stale preview (or an open LIVE confirm) makes no sense once the
  // override inputs change — the matching dry-run for the NEW values hasn't
  // run yet, so collapse back to "needs a fresh preview".
  useEffect(() => {
    setPreviewOpen(false);
    setConfirmLive(false);
  }, [kNum, maxPosNum, lookbackNum, maxTotalNum]);

  async function run(action: string, req: Parameters<typeof createXsecJob>[0], okKey: string) {
    if (!canWrite || busy) return;
    setBusy(action);
    setMsg(null);
    try {
      const res = await createXsecJob(req);
      setMsg({ kind: 'ok', text: t(okKey, { id: res.job_id }) });
      onJobCreated(res.job_id);
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        const body = e.body as XSecApiErrorBody | undefined;
        const runningId = body?.running_job_id ?? jobs.find((j) => j.status === 'running')?.id ?? null;
        onConflict(runningId);
      }
      setMsg({ kind: 'err', text: errText(e, t) });
    } finally {
      setBusy(null);
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

  // Load a suggested (universe/min-contract-aware) config into the shared
  // inputs. Turns off auto-distribute so the naive capital/(2k) doesn't
  // overwrite it.
  function applyAssisted(r: SuggestParams) {
    setAutoDistribute(false);
    setK(String(r.k));
    setLookback(String(r.lookback));
    setMaxPosUsd(r.max_pos_usd.toFixed(2));
    setMaxTotalUsd(r.max_total_usd.toFixed(2));
  }

  const { data: autoconfigJob } = useSWR<XSecJob>(
    autoconfigJobId != null ? `xsec:autoconfig:${autoconfigJobId}` : null,
    () => getXsecJob(autoconfigJobId as number),
    { refreshInterval: (latest) => (latest && latest.status !== 'running' ? 0 : 2_000), revalidateOnFocus: false },
  );
  const autoconfigSettled = autoconfigJob != null && autoconfigJob.status !== 'running';
  const autoconfigResult = autoconfigSettled ? parseAutoconfigResult(autoconfigJob.output) : null;

  // Load a feasible autoconfig into the shared inputs once per job.
  useEffect(() => {
    if (!autoconfigResult || autoconfigJobId == null || autoconfigApplied === autoconfigJobId) return;
    setAutoconfigApplied(autoconfigJobId);
    if (!autoconfigResult.feasible) return;
    setAutoDistribute(false);
    setK(String(autoconfigResult.k));
    setLookback(String(autoconfigResult.lookback));
    setMaxPosUsd(autoconfigResult.max_pos_usd.toFixed(2));
    setMaxTotalUsd(autoconfigResult.max_total_usd.toFixed(2));
  }, [autoconfigResult, autoconfigJobId, autoconfigApplied]);

  async function runAutoconfig() {
    if (!canWrite || autoconfigBusy || anyRunning || busy) return;
    const util = parseFloat(assistUtil);
    if (!isFinite(util) || util <= 0 || util > 1) { setAutoconfigErr(t('assist_input_invalid')); return; }
    setAutoconfigBusy(true);
    setAutoconfigErr(null);
    setAutoconfigJobId(null);
    try {
      // capital omitted: the backend reads the real futures equity.
      const res = await createXsecJob({ mode: 'autoconfig', utilization: util });
      setAutoconfigJobId(res.job_id);
      onJobCreated(res.job_id);
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        const body = e.body as XSecApiErrorBody | undefined;
        onConflict(body?.running_job_id ?? jobs.find((j) => j.status === 'running')?.id ?? null);
      }
      setAutoconfigErr(errText(e, t));
    } finally {
      setAutoconfigBusy(false);
    }
  }

  function validAssistInputs(): { cap: number; lev: number; util: number } | null {
    const cap = parseFloat(capital), lev = LIVE_LEVERAGE, util = parseFloat(assistUtil);
    if (!isFinite(cap) || cap < 10 || cap > 10_000_000 ||
        !isFinite(lev) || lev < 1 || lev > 20 ||
        !isFinite(util) || util <= 0 || util > 1) return null;
    return { cap, lev, util };
  }

  // "Asistido": compute the best feasible config for this capital and load it.
  async function runAssist() {
    const v = validAssistInputs();
    if (!v) { setAssistErr(t('assist_input_invalid')); return; }
    setAssistBusy(true);
    setAssistErr(null);
    try {
      const r = await getXsecSuggestParams(v.cap, v.lev, v.util);
      setAssist(r);
      applyAssisted(r);
    } catch (e) {
      setAssistErr(errText(e, t));
    } finally {
      setAssistBusy(false);
    }
  }

  // "Resize & complete": recompute the feasible sizing, load it, and kick off a
  // dry-run so the operator confirms the incremental live fill. rebalance-live
  // keeps filled legs, resizes them to the new per-leg, and opens the missing
  // ones — at a size big enough that the min-contract-blocked legs now fit.
  // Real money still passes through the dry → Continue → Confirm gate.
  async function runComplete() {
    const v = validAssistInputs();
    if (!v) { setMsg({ kind: 'err', text: t('assist_input_invalid') }); return; }
    setAssistBusy(true);
    setAssistErr(null);
    try {
      const r = await getXsecSuggestParams(v.cap, v.lev, v.util);
      setAssist(r);
      applyAssisted(r);
      if (isLegBelowMin(r.max_pos_usd)) {
        setMsg({ kind: 'err', text: t('min_leg_warning', { per: r.max_pos_usd.toFixed(2), min: XSEC_MIN_LEG_USD }) });
        return;
      }
      await run(
        'dry',
        { mode: 'rebalance-dry', k: r.k, max_pos_usd: r.max_pos_usd, lookback: r.lookback, max_total_usd: r.max_total_usd },
        'msg_complete_dry',
      );
      // Open the preview AFTER the input-change effect (which resets it) has run.
      setPreviewOpen(true);
    } catch (e) {
      setMsg({ kind: 'err', text: errText(e, t) });
    } finally {
      setAssistBusy(false);
    }
  }

  // One boolean per button, passed to BOTH `disabled` and btnStyle() so the
  // look can never drift from the behaviour.
  const syncDisabled = !canWrite || anyRunning || busy != null;
  const assistDisabled = !canWrite || assistBusy || anyRunning || busy != null;
  const autoconfigDisabled = !canWrite || autoconfigBusy || assistBusy || anyRunning || busy != null;
  const previewDisabled = !canWrite || anyRunning || busy != null || !inputsValid || legTooSmall;
  const liveConfirmDisabled = busy != null || !liveEnabled;
  const cancelOrderDisabled = !canWrite || anyRunning || busy != null || orderId.trim().length === 0;
  const closeAllDisabled = !canWrite || anyRunning || busy != null;
  const closeAllConfirmDisabled = busy != null;
  const relaunchDisabled = !canWrite || anyRunning || busy != null || !inputsValid || legTooSmall || !relaunchArmed;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {/* Sync book */}
      <ActionRow
        title={t('act_sync_title')}
        sub={t('act_sync_sub')}
      >
        <button
          type="button"
          disabled={syncDisabled}
          suppressHydrationWarning
          onClick={() => run('sync', { mode: 'sync-book' }, 'msg_sync_created')}
          style={btnStyle('neutral', syncDisabled)}
        >
          {busy === 'sync' ? t('working') : t('btn_sync')}
        </button>
      </ActionRow>

      {/* Capital → auto-distribution across 2*k legs */}
      <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', alignItems: 'flex-end' }}>
        <NumField
          label={t('field_capital')}
          value={capital}
          onChange={(v) => { setCapitalTouched(true); setCapital(v); }}
          disabled={!canWrite}
        />
        {!autoDistribute && (
          <button
            type="button"
            onClick={() => setAutoDistribute(true)}
            disabled={!canWrite}
            suppressHydrationWarning
            style={{ ...inputStyle, cursor: canWrite ? 'pointer' : 'not-allowed', color: 'var(--brand)' }}
          >
            {t('btn_reset_auto')}
          </button>
        )}
      </div>
      {capitalValid && isFinite(kNum) && kNum > 0 && (
        <div className="mono" style={{ fontSize: 11, color: 'var(--tx-dim)' }}>
          {t('distribute_preview', {
            legs: kNum * 2,
            perLeg: (capitalNum / (2 * kNum)).toFixed(2),
            total: capitalNum.toFixed(2),
          })}
          {' · '}{t('auto_distribute_hint')}
        </div>
      )}

      {/* Assisted sizing — universe & min-contract aware. Loads a FEASIBLE
          config into the inputs below (unlike the naive capital/2k split). */}
      <div style={{ padding: 12, borderRadius: 'var(--r)', border: '1px solid var(--brand)', background: 'rgba(108,140,255,.04)' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
          <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--tx-hi)' }}>{t('assist_title')}</span>
          <span className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)' }}>{t('assist_live_subtitle')}</span>
        </div>
        <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', alignItems: 'flex-end' }}>
          <span className="mono" data-testid="assist-leverage-fixed" style={{ fontSize: 11, color: 'var(--tx-dim)', paddingBottom: 8 }}>
            {t('assist_leverage_fixed')}
          </span>
          <NumField label={t('assist_util')} value={assistUtil} onChange={setAssistUtil} disabled={!canWrite} />
          <button
            type="button"
            disabled={assistDisabled}
            suppressHydrationWarning
            onClick={() => void runAssist()}
            style={btnStyle('brand', assistDisabled)}
          >
            {assistBusy ? t('working') : t('assist_button')}
          </button>
          <button
            type="button"
            data-testid="autoconfig-btn"
            disabled={autoconfigDisabled}
            suppressHydrationWarning
            onClick={() => void runAutoconfig()}
            style={btnStyle('brand', autoconfigDisabled)}
          >
            {autoconfigBusy || (autoconfigJobId != null && !autoconfigSettled) ? t('working') : t('autoconfig_button')}
          </button>
        </div>
        <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', marginTop: 6 }}>{t('autoconfig_hint')}</div>
        {autoconfigErr && (
          <div className="mono" style={{ fontSize: 11, color: 'var(--down)', marginTop: 8 }}>{autoconfigErr}</div>
        )}
        {autoconfigSettled && autoconfigJob.status === 'error' && !autoconfigResult && (
          <div className="mono" style={{ fontSize: 11, color: 'var(--down)', marginTop: 8 }}>{t('autoconfig_failed', { id: autoconfigJob.id })}</div>
        )}
        {autoconfigResult && <AutoconfigCard result={autoconfigResult} />}
        {assistErr && (
          <div className="mono" style={{ fontSize: 11, color: 'var(--down)', marginTop: 8 }}>{assistErr}</div>
        )}
        {assist && (
          <div style={{ marginTop: 10, display: 'flex', flexDirection: 'column', gap: 6 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
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
                {assist.universe_size >= 0 ? t('assist_universe', { n: assist.universe_size }) : t('assist_universe_unknown')}
              </span>
              <span className="mono" style={{ fontSize: 10, color: 'var(--tx-dim)' }}>
                {t('assist_loaded', { k: assist.k, per: `$${assist.max_pos_usd.toFixed(2)}`, total: `$${assist.max_total_usd.toFixed(2)}` })}
              </span>
            </div>
            {assist.warnings.map((w) => (
              <div key={w} className="mono" style={{ fontSize: 10, color: 'var(--tx-dim)' }}>· {warnLabel(w)}</div>
            ))}
          </div>
        )}
      </div>

      {/* Shared rebalance override inputs (same values gate dry → LIVE) */}
      <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', alignItems: 'flex-end' }}>
        <NumField label={t('field_k')} value={k} onChange={setK} disabled={!canWrite} />
        <NumField label={t('field_max_pos_usd')} value={maxPosUsd} onChange={setMaxPosUsdManual} disabled={!canWrite} />
        <NumField label={t('field_lookback')} value={lookback} onChange={setLookback} disabled={!canWrite} />
        <NumField label={t('field_max_total_usd')} value={maxTotalUsd} onChange={setMaxTotalUsdManual} disabled={!canWrite} />
      </div>

      {/* Futures margin context */}
      {overview && (
        <div
          className="mono"
          style={{
            display: 'flex', gap: 16, flexWrap: 'wrap', alignItems: 'center',
            fontSize: 11, padding: '8px 12px', borderRadius: 'var(--r)',
            border: `1px solid ${marginWarning ? 'var(--warn)' : 'var(--line)'}`,
            background: marginWarning ? 'rgba(217,164,65,.06)' : 'transparent',
          }}
        >
          <span style={{ color: 'var(--tx-dim)' }}>
            {t('margin_available_label')}{' '}
            <span style={{ color: 'var(--tx-hi)' }}>${(availableMargin ?? 0).toFixed(2)}</span>
          </span>
          <span style={{ color: 'var(--tx-dim)' }}>
            {t('margin_equity_label')}{' '}
            <span style={{ color: 'var(--tx-hi)' }}>${(futuresEquity ?? 0).toFixed(2)}</span>
          </span>
          {marginWarning && (
            <span style={{ color: 'var(--warn)', fontWeight: 600 }}>
              {t('margin_warning', {
                side: (kNum * maxPosNum).toFixed(2),
                equity: (futuresEquity ?? 0).toFixed(2),
              })}
            </span>
          )}
        </div>
      )}

      {legTooSmall && (
        <div
          className="mono"
          role="alert"
          data-testid="min-leg-warning"
          style={{ fontSize: 11, padding: '8px 12px', borderRadius: 'var(--r)', border: '1px solid var(--down)', color: 'var(--down)', background: 'rgba(233,90,90,.06)', fontWeight: 600 }}
        >
          {t('min_leg_warning', { per: maxPosNum.toFixed(2), min: XSEC_MIN_LEG_USD })}
        </div>
      )}

      {/* Preview (analysis) — runs a rebalance-dry job against real prices
          and the real exchange book, then shows the structured plan below. */}
      <ActionRow title={t('act_dry_title')} sub={t('act_dry_sub')}>
        <button
          type="button"
          disabled={previewDisabled}
          suppressHydrationWarning
          onClick={async () => {
            setPreviewOpen(true);
            await run(
              'dry',
              { mode: 'rebalance-dry', k: kNum, max_pos_usd: maxPosNum, lookback: lookbackNum, max_total_usd: maxTotalNum },
              'msg_dry_created',
            );
          }}
          style={btnStyle('brand', previewDisabled)}
        >
          {busy === 'dry' ? t('working') : t('btn_preview')}
        </button>
      </ActionRow>

      {previewOpen && matchingDryRun && matchingDryRun.status === 'done' && !confirmLive && (
        <div style={{ padding: 14, borderRadius: 'var(--r)', border: '1px solid var(--line)' }}>
          <RebalancePreview
            job={matchingDryRun}
            onCancel={() => setPreviewOpen(false)}
            onContinue={() => setConfirmLive(true)}
          />
        </div>
      )}
      {previewOpen && (!matchingDryRun || matchingDryRun.status === 'running') && (
        <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{t('working')}</div>
      )}

      {/* Live rebalance confirm — only reachable via "Continuar" above, once
          a fresh matching preview exists. Real money always stays behind
          this explicit second confirmation. */}
      {confirmLive && (
        <div style={{ padding: 14, borderRadius: 'var(--r)', border: '1px solid var(--down)', background: 'rgba(233,90,90,.05)' }}>
          <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--down)' }}>{t('act_live_title')}</div>
          <div style={{ fontSize: 11, color: 'var(--tx-dim)', marginTop: 2, marginBottom: 12 }}>
            {matchingDryRun ? t('act_live_ready') : t('act_live_needs_dry')}
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
            <span style={{ fontSize: 12, color: 'var(--down)', fontWeight: 600 }}>{t('live_confirm_warning')}</span>
            <button
              type="button"
              disabled={liveConfirmDisabled}
              suppressHydrationWarning
              onClick={async () => {
                setConfirmLive(false);
                await run(
                  'live',
                  { mode: 'rebalance-live', k: kNum, max_pos_usd: maxPosNum, lookback: lookbackNum, max_total_usd: maxTotalNum },
                  'msg_live_created',
                );
              }}
              style={btnStyle('down', liveConfirmDisabled)}
            >
              {busy === 'live' ? t('working') : t('live_confirm_btn')}
            </button>
            <button
              type="button"
              onClick={() => { setConfirmLive(false); setPreviewOpen(true); }}
              style={{ padding: '8px 14px', borderRadius: 'var(--r)', border: '1px solid var(--line)', background: 'transparent', color: 'var(--tx-dim)', fontSize: 12, cursor: 'pointer' }}
            >
              {t('cancel')}
            </button>
          </div>
        </div>
      )}

      {/* Last live-deploy result — why it entered partial + resize & complete.
          Only shown when the latest live deploy left missing legs. */}
      {deployResult && deployResult.missingCount > 0 && (
        <div style={{ padding: 14, borderRadius: 'var(--r)', border: '1px solid var(--warn)', background: 'rgba(217,164,65,.05)' }}>
          <div className="mono" style={{ fontSize: 10, color: 'var(--warn)', textTransform: 'uppercase', letterSpacing: '.06em', marginBottom: 10 }}>
            {t('rr_title', { id: lastDeployId ?? 0 })}
          </div>
          <RebalanceResultReport
            result={deployResult}
            onComplete={() => void runComplete()}
            completing={assistBusy}
            completeDisabled={!canWrite || anyRunning || busy != null || !capitalValid}
          />
        </div>
      )}

      {/* Cancel order */}
      <ActionRow title={t('act_cancel_title')} sub={t('act_cancel_sub')}>
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
          <input
            value={orderId}
            onChange={(e) => setOrderId(e.target.value)}
            placeholder={t('field_order_id')}
            disabled={!canWrite}
            suppressHydrationWarning
            style={inputStyle}
          />
          <button
            type="button"
            disabled={cancelOrderDisabled}
            suppressHydrationWarning
            onClick={() => run(
              'cancel',
              { mode: 'cancel-order', order_id: orderId.trim() },
              'msg_cancel_created',
            )}
            style={btnStyle('neutral', cancelOrderDisabled)}
          >
            {busy === 'cancel' ? t('working') : t('btn_cancel_order')}
          </button>
        </div>
      </ActionRow>

      {/* Danger zone: liquidate everything / start over. Real money. */}
      <div style={{ padding: 12, borderRadius: 'var(--r)', border: '1px solid var(--down)', background: 'rgba(233,90,90,.05)' }}>
        <div className="mono" style={{ fontSize: 10, color: 'var(--down)', textTransform: 'uppercase', letterSpacing: '.08em', marginBottom: 10 }}>
          {t('danger_zone')}
        </div>

        {/* Liquidar todo */}
        <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', marginBottom: 12 }}>
          <div style={{ flex: 1, minWidth: 200 }}>
            <div style={{ fontSize: 12, color: 'var(--tx-hi)', fontWeight: 600 }}>{t('act_close_all_title')}</div>
            <div style={{ fontSize: 11, color: 'var(--tx-dim)' }}>{t('act_close_all_sub')}</div>
            <div style={{ fontSize: 11, color: 'var(--tx-dim)', marginTop: 2 }}>{t('act_close_all_scope_note')}</div>
            {nonBasketPositions.length > 0 && (
              <div role="alert" style={{ marginTop: 6, padding: '6px 8px', borderRadius: 'var(--r)', border: '1px solid var(--warn, #e9a23e)', fontSize: 11, color: 'var(--tx)' }}>
                <div style={{ fontWeight: 600 }}>{t('act_close_all_outside', { count: nonBasketPositions.length })}</div>
                <div className="mono" style={{ marginTop: 2 }}>
                  {nonBasketPositions.map((p) => `${p.symbol} ${p.side} ${p.size} (${p.unrealized_pnl >= 0 ? '+' : ''}${p.unrealized_pnl.toFixed(4)})`).join(' · ')}
                </div>
                <div style={{ marginTop: 2 }}>{t('act_close_all_use_killswitch')}</div>
              </div>
            )}
          </div>
          {!confirmCloseAll ? (
            <button
              type="button"
              disabled={closeAllDisabled}
              suppressHydrationWarning
              onClick={() => setConfirmCloseAll(true)}
              style={btnStyle('down', closeAllDisabled)}
            >
              {t('btn_close_all')}
            </button>
          ) : (
            <div style={{ display: 'flex', gap: 6 }}>
              <button
                type="button"
                disabled={closeAllConfirmDisabled}
                suppressHydrationWarning
                onClick={() => { setConfirmCloseAll(false); run('close-all', { mode: 'close-all' }, 'msg_close_all_created'); }}
                style={btnStyle('down', closeAllConfirmDisabled)}
              >
                {busy === 'close-all' ? t('working') : t('confirm_close_all')}
              </button>
              <button type="button" onClick={() => setConfirmCloseAll(false)} style={btnStyle('neutral', false)}>
                {t('cancel')}
              </button>
            </div>
          )}
        </div>

        {/* Empezar de nuevo */}
        <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
          <div style={{ flex: 1, minWidth: 200 }}>
            <div style={{ fontSize: 12, color: 'var(--tx-hi)', fontWeight: 600 }}>{t('act_relaunch_title')}</div>
            <div style={{ fontSize: 11, color: 'var(--tx-dim)' }}>{t('act_relaunch_sub')}</div>
          </div>
          <input
            value={relaunchConfirmText}
            onChange={(e) => setRelaunchConfirmText(e.target.value)}
            placeholder={t('relaunch_confirm_placeholder')}
            disabled={!canWrite}
            suppressHydrationWarning
            style={inputStyle}
          />
          <button
            type="button"
            disabled={relaunchDisabled}
            suppressHydrationWarning
            onClick={() => { run('relaunch', { mode: 'relaunch', k: kNum, max_pos_usd: maxPosNum, lookback: lookbackNum, max_total_usd: maxTotalNum }, 'msg_relaunch_created'); setRelaunchConfirmText(''); }}
            style={btnStyle('down', relaunchDisabled)}
          >
            {busy === 'relaunch' ? t('working') : t('btn_relaunch')}
          </button>
        </div>
      </div>

      {anyRunning && (
        <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{t('another_job_running')}</div>
      )}

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
    </div>
  );
}

function ActionRow({ title, sub, children }: { title: string; sub: string; children: React.ReactNode }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12, flexWrap: 'wrap', padding: 12, borderRadius: 'var(--r)', border: '1px solid var(--line)' }}>
      <div>
        <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--tx-hi)' }}>{title}</div>
        <div style={{ fontSize: 11, color: 'var(--tx-dim)', marginTop: 2 }}>{sub}</div>
      </div>
      {children}
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
        disabled={disabled}
        // Browser extensions (password managers, Dark Reader, autofill) mutate
        // form-control attributes like `disabled` before React hydrates, which
        // triggers a benign hydration-attribute warning even though the SSR
        // HTML is correct. Suppress it on the controls they touch.
        suppressHydrationWarning
        onChange={(e) => onChange(e.target.value)}
        style={{ ...inputStyle, width: 120 }}
      />
    </label>
  );
}

const inputStyle: React.CSSProperties = {
  padding: '7px 10px', borderRadius: 4, border: '1px solid var(--line)',
  background: 'var(--bg-void)', color: 'var(--tx-hi)', fontSize: 12,
};

function btnStyle(variant: 'up' | 'down' | 'neutral' | 'brand', disabled: boolean): React.CSSProperties {
  const bg = variant === 'up' ? 'var(--up)' : variant === 'down' ? 'var(--down)' : variant === 'brand' ? 'var(--brand)' : 'var(--bg-panel-2)';
  return {
    padding: '9px 18px', borderRadius: 'var(--r)', border: variant === 'neutral' ? '1px solid var(--line)' : 'none',
    background: disabled ? 'var(--bg-void)' : bg,
    color: disabled ? 'var(--tx-faint)' : variant === 'neutral' ? 'var(--tx-hi)' : 'var(--bg)',
    fontSize: 13, fontWeight: 600, cursor: disabled ? 'not-allowed' : 'pointer', whiteSpace: 'nowrap',
  };
}
