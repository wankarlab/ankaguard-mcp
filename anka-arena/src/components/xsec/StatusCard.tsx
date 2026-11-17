'use client';

// StatusCard — per-leg table of the current XSec basket (live PnL $ and %,
// holding duration, close-leg action) plus a compact params summary. KPI
// totals (equity, return, imbalance, drawdown) live in PerformanceOverview
// above this panel — this component focuses on the actionable leg-by-leg
// view. The basket/PnL read comes from the public GET /xsec/status endpoint
// (no JWT needed to view); closing a leg posts a 'close-leg' job through the
// same async job runner ActionsPanel uses, so it requires canWrite (JWT +
// Level 2, enforced server-side too).

import { useMemo, useState } from 'react';
import { useTranslations } from 'next-intl';
import useSWR from 'swr';
import { Badge } from '@/components/ui/Badge';
import { ApiError } from '@/lib/api';
import { getXsecStatus, createXsecJob, getXsecJob, type XSecStatus, type XSecJob, type XSecApiErrorBody } from '@/lib/xsec-api';
import { computeNeutralization } from '@/lib/xsec-neutralize';

// A leg whose live PnL breaches this fraction is flagged as a blow-out risk —
// a single leg bleeding (like ALLO −24.9% on 2026-07-25) drags the whole book
// and is the first candidate to review/close.
const ADVERSE_PNL_PCT = -15;

// Poll a job until it leaves the running/queued state (or times out). Used by
// the sequential re-neutralize action, which must wait for each close-leg job
// to settle before starting the next (the server enforces a single-flight lock
// across all xsec jobs, so overlapping closes would 409).
async function waitForJobSettled(id: number, timeoutMs = 90_000): Promise<XSecJob> {
  const start = Date.now();
  for (;;) {
    const j = await getXsecJob(id);
    if (j.status === 'done' || j.status === 'error') return j;
    if (Date.now() - start > timeoutMs) throw new Error('timeout');
    await new Promise((r) => setTimeout(r, 1500));
  }
}

function fmtUsd(n: number): string {
  if (!isFinite(n)) return '—';
  return `$${n.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

function fmtTime(iso?: string): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (isNaN(d.getTime()) || d.getUTCFullYear() < 2000) return '—';
  return d.toLocaleString();
}

function fmtPct(n: number): string {
  if (!isFinite(n)) return '—';
  const sign = n > 0 ? '+' : '';
  return `${sign}${n.toFixed(2)}%`;
}

// Only ever rendered after the client-side SWR fetch resolves (never during
// SSR, since positions is [] until then), so Date.now() here can't disagree
// with a server-rendered timestamp — no hydration mismatch risk.
function fmtDuration(iso?: string): string {
  if (!iso) return '—';
  const opened = new Date(iso).getTime();
  if (isNaN(opened)) return '—';
  const ms = Date.now() - opened;
  if (ms < 0) return '—';
  const mins = Math.floor(ms / 60_000);
  if (mins < 60) return `${mins}m`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h ${mins % 60}m`;
  const days = Math.floor(hours / 24);
  return `${days}d ${hours % 24}h`;
}

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
  canWrite?: boolean;
  anyRunning?: boolean;
  onJobCreated?: (jobId: number) => void;
  onConflict?: (runningJobId: number | null) => void;
}

export function StatusCard({ canWrite = false, anyRunning = false, onJobCreated, onConflict }: Props) {
  const t = useTranslations('Xsec');

  const { data, isLoading, error } = useSWR<XSecStatus>(
    'xsec:status',
    getXsecStatus,
    { revalidateOnFocus: false, refreshInterval: 15_000 },
  );

  // Best-to-worst by live PnL $ — legs with unknown PnL (exchange enrichment
  // missed them, unrealized_pnl absent) sort last, never mixed in as if they
  // were flat.
  const positions = [...(data?.positions ?? [])].sort((a, b) => {
    if (a.unrealized_pnl == null && b.unrealized_pnl == null) return 0;
    if (a.unrealized_pnl == null) return 1;
    if (b.unrealized_pnl == null) return -1;
    return b.unrealized_pnl - a.unrealized_pnl;
  });
  const nLong = positions.filter((p) => p.dir > 0).length;
  const nShort = positions.filter((p) => p.dir < 0).length;
  const totals = data?.totals;

  // Neutralization advisory: when the live book carries directional beta,
  // recommend the exact minimal set of legs to close to restore neutrality.
  const advice = useMemo(() => computeNeutralization(positions), [positions]);
  const suggestedPairs = useMemo(
    () => new Set((advice?.suggested ?? []).map((p) => p.pair)),
    [advice],
  );

  const [closingPair, setClosingPair] = useState<string | null>(null);
  const [confirmPair, setConfirmPair] = useState<string | null>(null);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  // Re-neutralize (sequential close of the advised legs) state.
  const [reNeutralConfirm, setReNeutralConfirm] = useState(false);
  const [reNeutralProgress, setReNeutralProgress] = useState<{ done: number; total: number } | null>(null);

  // Hybrid one-click apply: fire the server-side 'neutralize' job. The backend
  // reads the LIVE exchange book, computes the minimal heavy-side set to close
  // (single source of truth — more robust than firing N sequential close-leg
  // jobs from the client), and closes them reduce-only in one job. The client
  // advisory above is the preview; the server decides the exact set.
  async function reNeutralize() {
    setReNeutralConfirm(false);
    setMsg(null);
    setReNeutralProgress({ done: 0, total: 1 });
    try {
      const res = await createXsecJob({ mode: 'neutralize' });
      onJobCreated?.(res.job_id);
      const settled = await waitForJobSettled(res.job_id);
      setMsg(
        settled.status === 'done'
          ? { kind: 'ok', text: t('neutralize_job_done', { id: settled.id }) }
          : { kind: 'err', text: t('neutralize_job_failed', { id: settled.id }) },
      );
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        const body = e.body as XSecApiErrorBody | undefined;
        onConflict?.(body?.running_job_id ?? null);
      }
      setMsg({ kind: 'err', text: errText(e, t) });
    } finally {
      setReNeutralProgress(null);
    }
  }

  async function closeLeg(pair: string) {
    setConfirmPair(null);
    setClosingPair(pair);
    setMsg(null);
    try {
      const res = await createXsecJob({ mode: 'close-leg', pair });
      setMsg({ kind: 'ok', text: t('msg_close_created', { id: res.job_id, pair }) });
      onJobCreated?.(res.job_id);
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        const body = e.body as XSecApiErrorBody | undefined;
        onConflict?.(body?.running_job_id ?? null);
      }
      setMsg({ kind: 'err', text: errText(e, t) });
    } finally {
      setClosingPair(null);
    }
  }

  if (isLoading) {
    return <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)', padding: 16 }}>{t('loading')}</div>;
  }
  if (error) {
    return <div className="mono" style={{ fontSize: 12, color: 'var(--down)', padding: 16 }}>{t('load_error')}</div>;
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
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

      {/* Consolidated total — sum across the whole basket, not per-leg. */}
      {positions.length > 0 && totals && (
        <div
          style={{
            display: 'flex', gap: 18, flexWrap: 'wrap', padding: '10px 14px',
            borderRadius: 'var(--r)', border: '1px solid var(--line)', background: 'var(--bg-inset)',
          }}
        >
          <div>
            <div className="mono" style={{ fontSize: 10, letterSpacing: '.06em', color: 'var(--tx-faint)', textTransform: 'uppercase' }}>
              {t('total_notional_label')}
            </div>
            <div className="mono" style={{ fontSize: 15, color: 'var(--tx-hi)', fontWeight: 600 }}>
              {fmtUsd(totals.total_notional)}
            </div>
          </div>
          <div>
            <div className="mono" style={{ fontSize: 10, letterSpacing: '.06em', color: 'var(--tx-faint)', textTransform: 'uppercase' }}>
              {t('total_pnl_label')}
            </div>
            <div
              className="mono"
              style={{
                fontSize: 15, fontWeight: 600,
                color: totals.total_unrealized_pnl == null
                  ? 'var(--tx-faint)'
                  : totals.total_unrealized_pnl >= 0 ? 'var(--up)' : 'var(--down)',
              }}
            >
              {totals.total_unrealized_pnl == null ? '—' : fmtUsd(totals.total_unrealized_pnl)}
              {totals.total_unrealized_pnl_pct != null && (
                <span style={{ fontSize: 11, marginLeft: 6, fontWeight: 400 }}>
                  ({fmtPct(totals.total_unrealized_pnl_pct)})
                </span>
              )}
            </div>
          </div>
          {totals.known_legs < positions.length && (
            <div style={{ alignSelf: 'center' }}>
              <span className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)' }}>
                {t('total_partial_warning', { known: totals.known_legs, total: positions.length })}
              </span>
            </div>
          )}
        </div>
      )}

      {/* Neutralization advisory — only when the book is off-neutral. Names
          the minimal set of legs to close; those rows are highlighted below
          with a tag so the existing per-row close button is one click away. */}
      {advice && (
        <div
          style={{
            padding: '10px 14px', borderRadius: 'var(--r)',
            border: '1px solid var(--down)', background: 'rgba(233,90,90,.06)',
          }}
        >
          <div style={{ fontSize: 12, fontWeight: 700, color: 'var(--down)', marginBottom: 4 }}>
            {t('neutralize_title')}
          </div>
          <div className="mono" style={{ fontSize: 11, color: 'var(--tx-dim)' }}>
            {t('neutralize_detail', {
              pct: advice.imbalancePct.toFixed(1),
              side: advice.heavySide === 'long' ? t('neutralize_side_long') : t('neutralize_side_short'),
              net: Math.abs(advice.netExposure).toFixed(2),
              count: advice.suggested.length,
              result: advice.resultingPct.toFixed(1),
            })}
          </div>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginTop: 8 }}>
            {advice.suggested.map((p) => (
              <span
                key={p.pair}
                className="mono"
                style={{
                  fontSize: 11, padding: '3px 8px', borderRadius: 4,
                  border: '1px solid var(--down)', color: 'var(--tx-hi)',
                  background: 'rgba(233,90,90,.10)',
                }}
              >
                {p.pair}{' '}
                <span style={{ color: p.unrealized_pnl == null ? 'var(--tx-faint)' : p.unrealized_pnl >= 0 ? 'var(--up)' : 'var(--down)' }}>
                  {p.unrealized_pnl == null ? '' : fmtUsd(p.unrealized_pnl)}
                </span>
              </span>
            ))}
          </div>

          {/* One-click re-neutralize: close all advised legs in sequence
              (single-flight lock forces one at a time). Real money, so the
              same explicit second-confirm pattern as per-row close applies. */}
          <div style={{ marginTop: 10 }}>
            {reNeutralProgress ? (
              <span className="mono" style={{ fontSize: 11, color: 'var(--tx-dim)' }}>
                {t('neutralize_working')}
              </span>
            ) : reNeutralConfirm ? (
              <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
                <span style={{ fontSize: 11, color: 'var(--down)', fontWeight: 600 }}>
                  {t('neutralize_btn_confirm', { count: advice.suggested.length })}
                </span>
                <button
                  type="button"
                  disabled={!canWrite || anyRunning || closingPair != null} suppressHydrationWarning
                  onClick={() => void reNeutralize()}
                  style={closeBtnStyle('down', !canWrite || anyRunning || closingPair != null)}
                >
                  {t('neutralize_btn_go', { count: advice.suggested.length })}
                </button>
                <button
                  type="button"
                  onClick={() => setReNeutralConfirm(false)}
                  style={{ background: 'none', border: 'none', color: 'var(--tx-dim)', fontSize: 11, cursor: 'pointer', padding: '4px 6px' }}
                >
                  {t('cancel')}
                </button>
              </div>
            ) : (
              <button
                type="button"
                disabled={!canWrite || anyRunning || closingPair != null} suppressHydrationWarning
                onClick={() => setReNeutralConfirm(true)}
                style={closeBtnStyle('down', !canWrite || anyRunning || closingPair != null)}
              >
                {t('neutralize_btn', { count: advice.suggested.length })}
              </button>
            )}
          </div>
        </div>
      )}

      {/* Basket table */}
      <div>
        <div className="mono" style={{ fontSize: 10, letterSpacing: '.06em', color: 'var(--tx-faint)', textTransform: 'uppercase', marginBottom: 8 }}>
          {t('basket_label')} ({t('legs', { long: nLong, short: nShort })})
        </div>
        {positions.length === 0 ? (
          <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{t('no_basket')}</div>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
              <thead>
                <tr style={{ textAlign: 'left', color: 'var(--tx-faint)', fontSize: 10, textTransform: 'uppercase' }}>
                  <th style={{ padding: '4px 8px' }}>{t('col_pair')}</th>
                  <th style={{ padding: '4px 8px' }}>{t('col_side')}</th>
                  <th style={{ padding: '4px 8px', textAlign: 'right' }}>{t('col_entry')}</th>
                  <th style={{ padding: '4px 8px', textAlign: 'right' }}>{t('col_notional')}</th>
                  <th style={{ padding: '4px 8px', textAlign: 'right' }}>{t('col_pnl')}</th>
                  <th style={{ padding: '4px 8px', textAlign: 'right' }}>{t('col_pnl_pct')}</th>
                  <th style={{ padding: '4px 8px' }}>{t('col_duration')}</th>
                  <th style={{ padding: '4px 8px' }}>{t('col_opened_at')}</th>
                  <th style={{ padding: '4px 8px' }}>{t('col_close')}</th>
                </tr>
              </thead>
              <tbody>
                {positions.map((p) => (
                  <tr
                    key={p.pair}
                    style={{
                      borderTop: '1px solid var(--line)',
                      background: suggestedPairs.has(p.pair) ? 'rgba(233,90,90,.05)' : undefined,
                      boxShadow: suggestedPairs.has(p.pair) ? 'inset 2px 0 0 var(--down)' : undefined,
                    }}
                  >
                    <td className="mono" style={{ padding: '6px 8px', color: 'var(--tx-hi)' }}>
                      {p.pair}
                      {p.unrealized_pnl != null && p.notional > 0 && (p.unrealized_pnl / p.notional) * 100 <= ADVERSE_PNL_PCT && (
                        <span
                          className="mono"
                          title={t('leg_risk_title', { pct: Math.abs(ADVERSE_PNL_PCT) })}
                          style={{
                            marginLeft: 6, fontSize: 9, color: 'var(--down)', fontWeight: 700,
                            padding: '1px 4px', borderRadius: 3, border: '1px solid var(--down)',
                          }}
                        >
                          {t('leg_risk_flag')}
                        </span>
                      )}
                      {suggestedPairs.has(p.pair) && (
                        <span className="mono" style={{ marginLeft: 6, fontSize: 9, color: 'var(--down)' }}>
                          {t('neutralize_tag')}
                        </span>
                      )}
                    </td>
                    <td style={{ padding: '6px 8px' }}>
                      <Badge variant={p.dir > 0 ? 'ok' : 'real'}>{p.dir > 0 ? t('side_long') : t('side_short')}</Badge>
                    </td>
                    <td className="mono" style={{ padding: '6px 8px', textAlign: 'right', color: 'var(--tx-dim)' }}>{p.entry}</td>
                    <td className="mono" style={{ padding: '6px 8px', textAlign: 'right', color: 'var(--tx-hi)' }}>{fmtUsd(p.notional)}</td>
                    <td
                      className="mono"
                      style={{
                        padding: '6px 8px', textAlign: 'right',
                        color: p.unrealized_pnl == null ? 'var(--tx-faint)' : p.unrealized_pnl >= 0 ? 'var(--up)' : 'var(--down)',
                      }}
                    >
                      {p.unrealized_pnl == null ? '—' : fmtUsd(p.unrealized_pnl)}
                    </td>
                    <td
                      className="mono"
                      style={{
                        padding: '6px 8px', textAlign: 'right',
                        color: p.unrealized_pnl == null || !p.notional
                          ? 'var(--tx-faint)'
                          : p.unrealized_pnl >= 0 ? 'var(--up)' : 'var(--down)',
                      }}
                    >
                      {p.unrealized_pnl == null || !p.notional ? '—' : fmtPct((p.unrealized_pnl / p.notional) * 100)}
                    </td>
                    <td className="mono" style={{ padding: '6px 8px', color: 'var(--tx-dim)' }}>{fmtDuration(p.opened_at)}</td>
                    <td className="mono" style={{ padding: '6px 8px', color: 'var(--tx-faint)' }}>{fmtTime(p.opened_at)}</td>
                    <td style={{ padding: '6px 8px' }}>
                      {confirmPair === p.pair ? (
                        <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
                          <span style={{ fontSize: 10, color: 'var(--down)', fontWeight: 600, whiteSpace: 'nowrap' }}>
                            {t('close_confirm_warning')}
                          </span>
                          <button
                            type="button"
                            disabled={closingPair != null} suppressHydrationWarning
                            onClick={() => void closeLeg(p.pair)}
                            style={closeBtnStyle('down', closingPair != null)}
                          >
                            {closingPair === p.pair ? t('working') : t('close_confirm_btn')}
                          </button>
                          <button
                            type="button"
                            onClick={() => setConfirmPair(null)}
                            style={{ background: 'none', border: 'none', color: 'var(--tx-dim)', fontSize: 11, cursor: 'pointer', padding: '4px 6px' }}
                          >
                            {t('cancel')}
                          </button>
                        </div>
                      ) : (
                        <button
                          type="button"
                          disabled={!canWrite || anyRunning || closingPair != null} suppressHydrationWarning
                          onClick={() => setConfirmPair(p.pair)}
                          style={closeBtnStyle('neutral', !canWrite || anyRunning || closingPair != null)}
                        >
                          {t('btn_close_leg')}
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {/* Params summary (read-only mirror; edit via ParamsEditor below) */}
      <div>
        <div className="mono" style={{ fontSize: 10, letterSpacing: '.06em', color: 'var(--tx-faint)', textTransform: 'uppercase', marginBottom: 8 }}>
          {t('params_summary_label')}
        </div>
        {(data?.params ?? []).length === 0 ? (
          <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{t('no_params')}</div>
        ) : (
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
            {(data?.params ?? []).map((p) => (
              <span
                key={p.key}
                className="mono"
                style={{
                  fontSize: 11, padding: '4px 8px', borderRadius: 4,
                  border: '1px solid var(--line)', background: 'var(--bg-inset)', color: 'var(--tx-dim)',
                }}
              >
                {p.key}=<span style={{ color: 'var(--tx-hi)' }}>{p.value}</span>
              </span>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function closeBtnStyle(variant: 'down' | 'neutral', disabled: boolean): React.CSSProperties {
  const bg = variant === 'down' ? 'var(--down)' : 'var(--bg-panel-2)';
  return {
    padding: '5px 10px', borderRadius: 4, border: variant === 'neutral' ? '1px solid var(--line)' : 'none',
    background: disabled ? 'var(--bg-void)' : bg,
    color: disabled ? 'var(--tx-faint)' : variant === 'neutral' ? 'var(--tx-hi)' : 'var(--bg)',
    fontSize: 11, fontWeight: 600, cursor: disabled ? 'not-allowed' : 'pointer', whiteSpace: 'nowrap',
  };
}
