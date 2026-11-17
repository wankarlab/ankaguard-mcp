'use client';

// PromoteToLivePanel — "Run live with this configuration", shown below a
// finished SINGLE backtest. Takes k/lookback from the backtest job's argv and
// the per-leg / total sizing from GET /xsec/suggest-params, then walks the
// same dry-run-first path as ActionsPanel, inline (never navigates away):
//   idle → previewing (rebalance-dry) → confirming → live (rebalance-live)
// Real money: the live job is only reachable through the explicit confirm step
// AND the shared gate (findMatchingDryRun: same args, finished < 10 min ago).
// The server remains the authority (level 2+ and its own 412 gate).
//
// Leverage / margin utilization: the backtest job does NOT carry them (they
// only drive the "Asistido" suggestion form, never a cmd/xsec-sim flag). So
// capital is read from the job's `-capital` argv (falling back to the report's
// start capital), and leverage is fixed at 1x (the live executor never uses more) and
// utilization is an editable field (default 0.80).

import { useEffect, useMemo, useState } from 'react';
import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { ApiError } from '@/lib/api';
import { isKillSwitchActiveError } from '@/lib/killswitch-ring0';
import {
  getXsecJob,
  getXsecSuggestParams,
  computePlanBalance,
  parseBacktestReport,
  parseRebalancePlan,
  parseRebalanceResult,
  type XSecApiErrorBody,
  type XSecJob,
} from '@/lib/xsec-api';
import {
  canConfirmLive,
  evaluatePromoteBlock,
  listNotAppliedLive,
  parseBacktestJobArgs,
  PROMOTE_MAX_IMBALANCE_PCT,
  requestDryRun,
  requestLive,
  type PromoteJobArgs,
} from '@/lib/xsec-promote';
import { XSEC_MIN_LEG_USD } from '@/lib/xsec-live-gate';
import { RebalancePreview } from '@/components/xsec/RebalancePreview';
import { RebalanceWarnings } from '@/components/xsec/RebalanceWarnings';
import { RebalanceResultReport } from '@/components/xsec/RebalanceResultReport';

interface Props {
  /** The finished single backtest (full, non-truncated output). */
  job: XSecJob;
  /** Level 2+ (same gate ActionsPanel uses). */
  canWrite: boolean;
  anyRunning: boolean;
  /** Real futures account equity (USD); undefined while unknown (blocks promotion). */
  accountEquity?: number;
  /** Called after a job is created so the shared jobs poll refreshes. */
  onJobsChanged: () => void;
  onConflict: (runningJobId: number | null) => void;
}

type Phase = 'idle' | 'previewing' | 'confirming' | 'live';

const LIVE_LEVERAGE = 1;
const round2 = (n: number) => Number(n.toFixed(2));

function usd(n: number): string {
  return `$${n.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

function errText(e: unknown, t: ReturnType<typeof useTranslations>): string {
  if (isKillSwitchActiveError(e)) return `423 · ${t('err_killswitch')}`;
  if (e instanceof ApiError) {
    if (e.status === 403) return `403 · ${t('err_403')}`;
    if (e.status === 401) return `401 · ${t('err_401')}`;
    if (e.status === 412) return `412 · ${t('err_412')}`;
    const body = e.body as XSecApiErrorBody | undefined;
    if (e.status === 409) return `409 · ${body?.error?.message || t('err_conflict')}`;
    const detail = body?.error?.message || '';
    return `${e.status}${detail ? ` · ${detail}` : ''}`;
  }
  return t('action_error');
}

export function PromoteToLivePanel({ job, canWrite, anyRunning, accountEquity, onJobsChanged, onConflict }: Props) {
  const t = useTranslations('Xsec');

  const report = useMemo(() => parseBacktestReport(job.output), [job.output]);
  const cfg = useMemo(() => parseBacktestJobArgs(job.args), [job.args]);

  const [utilization, setUtilization] = useState('0.80');
  const [phase, setPhase] = useState<Phase>('idle');
  const [snapshot, setSnapshot] = useState<PromoteJobArgs | null>(null);
  const [dryJobId, setDryJobId] = useState<number | null>(null);
  const [liveJobId, setLiveJobId] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  // Re-render periodically so a dry-run that ages past 10 minutes disables
  // the confirm button without any other interaction.
  const [, setTick] = useState(0);
  useEffect(() => {
    const id = setInterval(() => setTick((n) => n + 1), 15_000);
    return () => clearInterval(id);
  }, []);

  const capital = cfg.capital ?? report?.startCapital ?? NaN;
  // The live executor (cmd/xsec-live) always trades at 1x.
  const levNum = LIVE_LEVERAGE;
  const utilNum = parseFloat(utilization);
  const sizingInputsValid =
    isFinite(capital) && capital >= 10 && capital <= 10_000_000 &&
    isFinite(levNum) && levNum >= 1 && levNum <= 20 &&
    isFinite(utilNum) && utilNum > 0 && utilNum <= 1;

  const { data: suggest, error: suggestError } = useSWR(
    sizingInputsValid ? `xsec:promote-suggest:${capital}:${levNum}:${utilNum}` : null,
    () => getXsecSuggestParams(capital, levNum, utilNum),
    { revalidateOnFocus: false },
  );

  const { data: dryJob } = useSWR<XSecJob>(
    dryJobId != null ? `xsec:promote-job:${dryJobId}` : null,
    () => getXsecJob(dryJobId as number),
    { refreshInterval: (latest) => (latest && latest.status !== 'running' ? 0 : 2_000), revalidateOnFocus: false },
  );
  const { data: liveJob } = useSWR<XSecJob>(
    liveJobId != null ? `xsec:promote-job:${liveJobId}` : null,
    () => getXsecJob(liveJobId as number),
    { refreshInterval: (latest) => (latest && latest.status !== 'running' ? 0 : 2_000), revalidateOnFocus: false },
  );

  if (!report) return null;

  const jobArgs: PromoteJobArgs | null =
    suggest && cfg.k !== undefined && cfg.lookback !== undefined
      ? {
          k: cfg.k,
          lookback: cfg.lookback,
          // Rounded to cents, like ActionsPanel's assisted sizing, so the
          // dry-run/live argv round-trips exactly through the gate.
          maxPosUsd: round2(suggest.max_pos_usd),
          maxTotalUsd: round2(suggest.max_total_usd),
        }
      : null;

  // Blocking applies to the START of the flow only: once our own dry/live job
  // is running, "another XSEC job running" would otherwise be self-inflicted.
  const block = evaluatePromoteBlock({
    report,
    canWrite,
    anyRunning,
    backtestK: cfg.k,
    backtestLookback: cfg.lookback,
    suggest: suggest ?? null,
    accountEquity,
    leverage: levNum,
    utilization: utilNum,
  });
  const notApplied = listNotAppliedLive(cfg);

  // A dry-run plan that is not market-neutral must never reach live.
  const dryPlan = dryJob?.status === 'done' ? parseRebalancePlan(dryJob.output) : null;
  const dryImbalance = dryPlan ? computePlanBalance(dryPlan) : null;
  const imbalanceBlocked = dryImbalance != null && dryImbalance.gross > 0 && dryImbalance.imbalancePct > PROMOTE_MAX_IMBALANCE_PCT;
  const imbalanceMsg = imbalanceBlocked
    ? t('promote_block_imbalance', { pct: dryImbalance.imbalancePct.toFixed(1), max: PROMOTE_MAX_IMBALANCE_PCT })
    : undefined;

  function reset() {
    setPhase('idle');
    setSnapshot(null);
    setDryJobId(null);
    setLiveJobId(null);
    setMsg(null);
  }

  async function startPreview() {
    if (block || busy || !jobArgs) return;
    setBusy(true);
    setMsg(null);
    try {
      const res = await requestDryRun(jobArgs);
      setSnapshot(jobArgs);
      setDryJobId(res.job_id);
      setPhase('previewing');
      onJobsChanged();
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        const body = e.body as XSecApiErrorBody | undefined;
        onConflict(body?.running_job_id ?? null);
      }
      setMsg(errText(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function confirmLive() {
    if (busy || !snapshot || imbalanceBlocked) return;
    setBusy(true);
    setMsg(null);
    try {
      const res = await requestLive(dryJob, snapshot);
      if (!res) {
        setMsg(t('promote_preview_stale'));
        setPhase('previewing');
        return;
      }
      setLiveJobId(res.job_id);
      setPhase('live');
      onJobsChanged();
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        const body = e.body as XSecApiErrorBody | undefined;
        onConflict(body?.running_job_id ?? null);
      }
      setMsg(errText(e, t));
    } finally {
      setBusy(false);
    }
  }

  const gateOk = snapshot != null && canConfirmLive(dryJob, snapshot);
  const dryDone = dryJob?.status === 'done';
  const dryExpired = phase !== 'idle' && dryDone && !gateOk;
  const liveResult = liveJob && liveJob.status !== 'running' ? parseRebalanceResult(liveJob.output) : null;

  // Single source of truth per button: used for BOTH `disabled` and the style.
  const previewDisabled = block != null || busy || !jobArgs;
  const confirmDisabled = busy || !gateOk || imbalanceBlocked;

  return (
    <div
      data-testid="promote-live"
      style={{ padding: 14, borderRadius: 'var(--r)', border: '1px solid var(--brand)', background: 'rgba(108,140,255,.04)', display: 'flex', flexDirection: 'column', gap: 12 }}
    >
      <div>
        <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--tx-hi)' }}>{t('promote_title')}</div>
        <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', marginTop: 2 }}>
          {t('promote_subtitle', { id: job.id })}
        </div>
      </div>

      {/* Sizing inputs (leverage/utilization are not part of the backtest job). */}
      <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', alignItems: 'flex-end' }}>
        <span className="mono" data-testid="promote-leverage-fixed" style={{ fontSize: 11, color: 'var(--tx-dim)', paddingBottom: 8 }}>
          {t('assist_leverage_fixed')}
        </span>
        <SmallField label={t('assist_util')} value={utilization} onChange={setUtilization} disabled={phase !== 'idle'} />
        <span className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)' }}>
          {t('promote_capital_from_job', { v: isFinite(capital) ? usd(capital) : '—' })}
        </span>
      </div>

      {/* Live values */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
        <div className="mono" style={{ fontSize: 10, letterSpacing: '.06em', color: 'var(--tx-faint)', textTransform: 'uppercase' }}>
          {t('promote_live_values')}
        </div>
        {jobArgs ? (
          <div className="mono" data-testid="promote-live-values" style={{ fontSize: 12, color: 'var(--tx-hi)' }}>
            k={jobArgs.k} · lb={jobArgs.lookback} · {t('promote_per_leg')} {usd(jobArgs.maxPosUsd)} · {t('promote_total')} {usd(jobArgs.maxTotalUsd)}
          </div>
        ) : (
          <div className="mono" style={{ fontSize: 11, color: 'var(--tx-dim)' }}>
            {suggestError ? errText(suggestError, t) : t('promote_sizing_pending')}
          </div>
        )}
        {suggest && cfg.k !== undefined && suggest.k !== cfg.k && (
          <div className="mono" style={{ fontSize: 11, color: suggest.k < cfg.k ? 'var(--down)' : 'var(--tx-dim)' }}>
            {t('promote_k_compare', { bt: cfg.k, live: suggest.k })}
          </div>
        )}
      </div>

      {/* Not applied live */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
        <div className="mono" style={{ fontSize: 10, letterSpacing: '.06em', color: 'var(--warn)', textTransform: 'uppercase' }}>
          {t('promote_not_applied_title')}
        </div>
        <div className="mono" style={{ fontSize: 11, color: 'var(--tx-dim)' }}>{t('promote_not_applied_note')}</div>
        {notApplied.length > 0 ? (
          <ul data-testid="promote-not-applied" style={{ margin: 0, paddingLeft: 18, fontSize: 11, color: 'var(--tx-hi)' }}>
            {notApplied.map((it) => (
              <li key={it.key} className="mono">{t(`promote_na_${it.key}`)}: {it.value}</li>
            ))}
          </ul>
        ) : (
          <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{t('promote_not_applied_none')}</div>
        )}
      </div>

      {/* Idle: block reason + preview button */}
      {phase === 'idle' && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {block && (
            <div
              role="alert"
              data-testid="promote-block-reason"
              className="mono"
              style={{ fontSize: 11, color: 'var(--down)', padding: '8px 10px', borderRadius: 'var(--r)', border: '1px solid var(--down)', background: 'rgba(233,90,90,.06)' }}
            >
              {block === 'k_clamped' && suggest && cfg.k !== undefined
                ? t('promote_block_k_clamped', { bt: cfg.k, live: suggest.k })
                : block === 'leg_below_min' && suggest
                  ? t('promote_block_leg_below_min', { leg: usd(suggest.max_pos_usd), min: usd(XSEC_MIN_LEG_USD) })
                  : block === 'exceeds_equity' && suggest && accountEquity !== undefined
                  ? t('promote_block_exceeds_equity', {
                      total: usd(suggest.max_total_usd),
                      cap: usd(accountEquity * levNum * utilNum),
                      equity: usd(accountEquity),
                      lev: levNum,
                      util: utilNum,
                    })
                  : t(`promote_block_${block}`)}
            </div>
          )}
          <div>
            <button
              type="button"
              data-testid="promote-preview-btn"
              disabled={previewDisabled}
              suppressHydrationWarning
              onClick={() => void startPreview()}
              style={btn('brand', previewDisabled)}
            >
              {busy ? t('working') : t('promote_btn_preview')}
            </button>
          </div>
        </div>
      )}

      {/* Previewing / confirming: dry-run output */}
      {(phase === 'previewing' || phase === 'confirming') && dryJob && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          {dryJob.status === 'running' && (
            <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{t('working')}</div>
          )}
          {dryJob.status === 'error' && (
            <div className="mono" style={{ fontSize: 11, color: 'var(--down)' }}>{t('promote_dry_failed', { id: dryJob.id })}</div>
          )}
          {dryDone && <RebalanceWarnings mode={dryJob.mode} output={dryJob.output} />}
          {dryDone && phase === 'previewing' && (
            <RebalancePreview job={dryJob} onCancel={reset} onContinue={() => setPhase('confirming')} continueDisabledReason={imbalanceMsg} />
          )}
          {dryExpired && (
            <div className="mono" data-testid="promote-stale" style={{ fontSize: 11, color: 'var(--warn)' }}>
              {t('promote_preview_stale')}
            </div>
          )}
        </div>
      )}

      {phase === 'confirming' && (
        <div style={{ padding: 14, borderRadius: 'var(--r)', border: '1px solid var(--down)', background: 'rgba(233,90,90,.05)' }}>
          <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--down)' }}>{t('promote_confirm_title')}</div>
          <div style={{ fontSize: 12, color: 'var(--down)', fontWeight: 600, margin: '6px 0 12px' }}>
            {t('promote_confirm_warning')}
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
            <button
              type="button"
              data-testid="promote-confirm-btn"
              disabled={confirmDisabled}
              suppressHydrationWarning
              onClick={() => void confirmLive()}
              style={btn('down', confirmDisabled)}
            >
              {busy ? t('working') : t('promote_btn_confirm')}
            </button>
            <button
              type="button"
              data-testid="promote-cancel-btn"
              onClick={() => setPhase('previewing')}
              style={{ padding: '8px 14px', borderRadius: 'var(--r)', border: '1px solid var(--line)', background: 'transparent', color: 'var(--tx-dim)', fontSize: 12, cursor: 'pointer' }}
            >
              {t('cancel')}
            </button>
          </div>
        </div>
      )}

      {/* Live result */}
      {phase === 'live' && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          {(!liveJob || liveJob.status === 'running') && (
            <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{t('working')}</div>
          )}
          {liveJob?.status === 'error' && (
            <div className="mono" style={{ fontSize: 11, color: 'var(--down)' }}>{t('promote_live_failed', { id: liveJob.id })}</div>
          )}
          {liveJob && liveJob.status !== 'running' && <RebalanceWarnings mode={liveJob.mode} output={liveJob.output} />}
          {liveResult && <RebalanceResultReport result={liveResult} />}
          {liveJob && liveJob.status !== 'running' && !liveResult && (
            <pre className="mono" style={{ margin: 0, fontSize: 11, whiteSpace: 'pre-wrap', maxHeight: 240, overflow: 'auto' }}>
              {liveJob.output}
            </pre>
          )}
          {liveJob && liveJob.status !== 'running' && (
            <div>
              <button type="button" onClick={reset} style={btn('brand', false)}>{t('promote_btn_done')}</button>
            </div>
          )}
        </div>
      )}

      {msg && <div className="mono" role="status" style={{ fontSize: 11, color: 'var(--down)' }}>{msg}</div>}
    </div>
  );
}

function SmallField({ label, value, onChange, disabled }: { label: string; value: string; onChange: (v: string) => void; disabled: boolean }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 11, color: 'var(--tx-dim)' }}>
      {label}
      <input
        type="number"
        value={value}
        disabled={disabled}
        suppressHydrationWarning
        onChange={(e) => onChange(e.target.value)}
        style={{ padding: '7px 10px', borderRadius: 4, border: '1px solid var(--line)', background: 'var(--bg-void)', color: 'var(--tx-hi)', fontSize: 12, width: 110 }}
      />
    </label>
  );
}

function btn(kind: 'brand' | 'down', disabled: boolean): React.CSSProperties {
  const bg = kind === 'down' ? 'var(--down)' : 'var(--brand)';
  return {
    padding: '9px 18px', borderRadius: 'var(--r)', border: 'none',
    background: disabled ? 'var(--bg-void)' : bg,
    color: disabled ? 'var(--tx-faint)' : 'var(--bg)',
    fontSize: 13, fontWeight: 600, cursor: disabled ? 'not-allowed' : 'pointer', whiteSpace: 'nowrap',
  };
}
