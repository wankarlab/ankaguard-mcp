'use client';

// Ring 0 (global stop) panels: dry-run preview, per-position result report and
// the "global stop active" banner with an in-UI reset confirmation.

import { useState } from 'react';
import { useSWRConfig } from 'swr';
import { useTranslations } from 'next-intl';
import type { Ring } from './types';
import { isProductEdition } from '@/lib/productMode';
import { apiErrorDetail, resetRing0, ring0HasFailures, type Ring0Position, type Ring0Result } from '@/lib/killswitch-ring0';

const RED = '#f0564f';
const AMBER = '#e9a23e';
const GREEN = '#4adb84';

const cell: React.CSSProperties = { padding: '3px 6px', textAlign: 'left', whiteSpace: 'nowrap' };

function PositionsTable({ positions, showResult }: { positions: Ring0Position[]; showResult: boolean }) {
  const t = useTranslations('KillSwitch');
  return (
    <div style={{ maxHeight: 170, overflow: 'auto', border: '1px solid var(--line)', borderRadius: 6 }}>
      <table className="mono" style={{ width: '100%', fontSize: 10.5, borderCollapse: 'collapse' }}>
        <thead>
          <tr style={{ color: 'var(--tx-faint)' }}>
            <th style={cell}>{t('col_symbol')}</th>
            <th style={cell}>{t('col_side')}</th>
            <th style={cell}>{t('col_size')}</th>
            <th style={cell}>{t('col_pnl')}</th>
            {showResult && <th style={cell}>{t('col_result')}</th>}
          </tr>
        </thead>
        <tbody>
          {positions.map((p, i) => (
            <tr key={`${p.symbol}-${i}`}>
              <td style={{ ...cell, color: 'var(--tx-hi)' }}>{p.symbol}</td>
              <td style={cell}>{p.side}</td>
              <td style={cell}>{p.size}</td>
              <td style={{ ...cell, color: p.unrealized_pnl < 0 ? 'var(--down)' : 'var(--up)' }}>
                {p.unrealized_pnl.toFixed(4)}
              </td>
              {showResult && (
                <td style={{ ...cell, color: p.result === 'failed' ? RED : GREEN, whiteSpace: 'normal' }}>
                  {p.result === 'failed' ? `${t('result_failed')}${p.error ? `: ${p.error}` : ''}` : t('result_closed')}
                </td>
              )}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function ListFailure({ r }: { r: Ring0Result }) {
  const t = useTranslations('KillSwitch');
  if (!r.positions_list_error) return null;
  return (
    <div role="alert" style={{ fontSize: 12, color: RED, marginBottom: 6 }}>
      {t('positions_list_failed', { error: r.positions_list_error })}
    </div>
  );
}

function EnginesList({ r }: { r: Ring0Result }) {
  const t = useTranslations('KillSwitch');
  const failed = Object.entries(r.engines_failed);
  // The product has a single bot (counted above): the platform's internal
  // engine list means nothing to its operator. Failures are still shown.
  const showStopped = !isProductEdition();
  return (
    <>
      {showStopped && (
        <div className="mono" style={{ fontSize: 11, color: 'var(--tx)', marginTop: 8 }}>
          {t('engines_stopped', { count: r.engines_stopped.length })}
          {r.engines_stopped.length > 0 ? `: ${r.engines_stopped.join(', ')}` : ''}
        </div>
      )}
      {failed.map(([name, err]) => (
        <div key={name} className="mono" style={{ fontSize: 11, color: RED }}>
          {t('engine_failed', { name, error: err })}
        </div>
      ))}
    </>
  );
}

export function Ring0Preview({
  ring, preview, loading, error, onContinue, onRetry, onCancel,
}: {
  ring: Ring;
  preview: Ring0Result | null;
  loading: boolean;
  error: string;
  onContinue: () => void;
  onRetry: () => void;
  onCancel: () => void;
}) {
  const t = useTranslations('KillSwitch');
  return (
    <div>
      <div className="mono" style={{ color: ring.color, fontSize: 11, letterSpacing: '.08em', marginBottom: 8 }}>
        {t(isProductEdition() ? 'product_stop_name' : 'ring0_name')} · {t('preview_title')}
      </div>
      {loading && <div style={{ fontSize: 12, color: 'var(--tx-faint)' }}>{t('preview_loading')}</div>}
      {error && (
        <div role="alert" style={{ fontSize: 12, color: RED, marginBottom: 8 }}>{error}</div>
      )}
      {preview && !loading && (
        <>
          <ListFailure r={preview} />
          <div style={{ fontSize: 12, color: 'var(--tx)', marginBottom: 6 }} hidden={!!preview.positions_list_error && preview.positions.length === 0}>
            {preview.positions.length > 0
              ? t('preview_positions', { count: preview.positions.length })
              : t('preview_no_positions')}
          </div>
          {preview.positions.length > 0 && <PositionsTable positions={preview.positions} showResult={false} />}
          {/* The product runs no automatic bot: a bot count would always read 0. */}
          {!isProductEdition() && (
            <div className="mono" style={{ fontSize: 11, color: 'var(--tx)', marginTop: 8 }}>
              {t('preview_bots', { count: preview.bots_stopped })}
            </div>
          )}
          <EnginesList r={preview} />
          <div className="mono" style={{ fontSize: 11, color: 'var(--tx)', marginTop: 2 }}>
            {t('preview_orders', { count: preview.orders_cancelled })}
          </div>
          <div style={{ fontSize: 11, color: AMBER, marginTop: 8 }}>{t('preview_note')}</div>
        </>
      )}
      <div style={{ display: 'flex', gap: 8, marginTop: 14 }}>
        <button className="btn ghost" style={{ flex: 1 }} onClick={onCancel}>{t('cancel')}</button>
        {error || !preview ? (
          <button className="btn" style={{ flex: 1 }} onClick={onRetry} disabled={loading}>{t('retry')}</button>
        ) : (
          <button className="btn danger" style={{ flex: 1 }} onClick={onContinue} disabled={loading}>
            {t('preview_continue')}
          </button>
        )}
      </div>
    </div>
  );
}

export function Ring0ResultPanel({ ring, result, onClose }: { ring: Ring; result: Ring0Result; onClose: () => void }) {
  const t = useTranslations('KillSwitch');
  const warn = ring0HasFailures(result);
  const product = isProductEdition();
  const color = warn ? AMBER : GREEN;
  return (
    <div>
      <div
        role={warn ? 'alert' : 'status'}
        style={{ padding: '8px 10px', borderRadius: 6, border: `1px solid ${color}`, color, fontSize: 12.5, fontWeight: 600 }}
      >
        {warn ? t('result_partial') : t('result_ok')}
      </div>
      <div style={{ marginTop: 6 }}><ListFailure r={result} /></div>
      {result.positions_failed > 0 && (
        <div style={{ fontSize: 12, color: RED, marginTop: 6 }}>
          {t('result_positions_failed', { count: result.positions_failed })}
        </div>
      )}
      <div className="mono" style={{ fontSize: 11, color: 'var(--tx)', marginTop: 8 }}>
        {product
          ? t('product_result_counts', {
              closed: result.positions_closed, failed: result.positions_failed, orders: result.orders_cancelled,
            })
          : t('result_counts', {
              closed: result.positions_closed, failed: result.positions_failed,
              bots: result.bots_stopped, orders: result.orders_cancelled,
            })}
      </div>
      {product && result.orders_failed > 0 && (
        <div className="mono" style={{ fontSize: 11, color: RED }}>
          {t('product_result_orders_failed', { orders: result.orders_failed })}
        </div>
      )}
      {!product && (result.bots_failed > 0 || result.orders_failed > 0) && (
        <div className="mono" style={{ fontSize: 11, color: RED }}>
          {t('result_other_failed', { bots: result.bots_failed, orders: result.orders_failed })}
        </div>
      )}
      <EnginesList r={result} />
      {result.positions.length > 0 && (
        <div style={{ marginTop: 8 }}>
          <PositionsTable positions={result.positions} showResult />
        </div>
      )}
      <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 8 }}>{t('result_blocked_note')}</div>
      <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', marginTop: 4 }}>
        {isProductEdition() ? t('product_stop_name') : ring.shortName} · {new Date().toLocaleTimeString()}
      </div>
      <button className="btn" style={{ marginTop: 12 }} onClick={onClose}>{t('close_console')}</button>
    </div>
  );
}

/** Prominent "global stop active" banner with an in-UI confirm before reset. */
export function Ring0ActiveBanner({ compact = false }: { compact?: boolean }) {
  const t = useTranslations('KillSwitch');
  const { mutate } = useSWRConfig();
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  async function doReset() {
    setBusy(true);
    setErr('');
    try {
      await resetRing0();
      setConfirming(false);
      await Promise.all([mutate('killswitch:status'), mutate('home:killswitch')]);
    } catch (e) {
      setErr(`${t('reset_error')}${apiErrorDetail(e) ? `: ${apiErrorDetail(e)}` : ''}`);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div
      role="alert"
      style={{
        padding: compact ? '8px 10px' : '10px 12px', borderRadius: 8, border: `2px solid ${RED}`,
        background: 'rgba(240,86,79,.12)', marginBottom: compact ? 8 : 0,
      }}
    >
      <div style={{ color: RED, fontWeight: 700, fontSize: compact ? 12 : 13 }}>{t('banner_title')}</div>
      {!compact && <div style={{ color: 'var(--tx)', fontSize: 11.5, marginTop: 3 }}>{t('banner_desc')}</div>}
      {err && <div style={{ color: RED, fontSize: 11, marginTop: 4 }}>{err}</div>}
      <div style={{ display: 'flex', gap: 6, marginTop: 8, flexWrap: 'wrap', alignItems: 'center' }}>
        {!confirming ? (
          <button type="button" className="btn sm" onClick={() => setConfirming(true)}>{t('reset_btn')}</button>
        ) : (
          <>
            <span style={{ fontSize: 11, color: 'var(--tx)' }}>{t('reset_confirm')}</span>
            <button type="button" className="btn danger sm" disabled={busy} onClick={doReset}>
              {busy ? t('resetting') : t('reset_confirm_btn')}
            </button>
            <button type="button" className="btn ghost sm" disabled={busy} onClick={() => setConfirming(false)}>
              {t('cancel')}
            </button>
          </>
        )}
      </div>
    </div>
  );
}
