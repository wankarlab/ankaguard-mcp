'use client';

// BotTable — live table of active bots (SWR-polled) with a per-row KILL action
// and a per-bot paper-mode toggle. Below the table it surfaces the master agent's
// most recent scan opportunities and the read-only xsec strategy status.

import { useState } from 'react';
import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { Badge } from '@/components/ui/Badge';
import { ConfirmInline } from '@/components/ui/ConfirmInline';
import { ApiError } from '@/lib/api';
import { isServiceUnavailable } from '@/lib/motor-availability';
import { ManagerUnavailableNotice } from './ManagerUnavailableNotice';
import {
  listBots,
  killBot,
  setBotPaperMode,
  getOpportunities,
  getXSecStatus,
  type BotInfo,
  type ListBotsResult,
  type OpportunitiesResult,
  type XSecStatus,
} from '@/lib/motor-api';

function errText(e: unknown, t: ReturnType<typeof useTranslations>): string {
  if (e instanceof ApiError) {
    if (e.status === 403) return `403 · ${t('err_403')}`;
    if (e.status === 404) return `404 · ${t('err_404')}`;
    const body = e.body as { error?: string; message?: string } | undefined;
    return `${e.status}${body?.message || body?.error ? ` · ${body?.message || body?.error}` : ''}`;
  }
  return t('action_error');
}

interface Props {
  canWrite: boolean;
}

export function BotTable({ canWrite }: Props) {
  const t = useTranslations('Motor');
  const tc = useTranslations('Confirm');

  // The bots list only exists when the bot manager is wired; otherwise the
  // backend answers 503 and we replace the table with an explanatory notice.
  // Polling is slowed down by SWR's error handling (no retry storm).
  const { data: bots, error: botsError, isLoading, mutate } = useSWR<ListBotsResult>(
    'motor:bots',
    listBots,
    { revalidateOnFocus: false, refreshInterval: 15_000, shouldRetryOnError: false },
  );
  const managerUnavailable = isServiceUnavailable(botsError);

  const { data: opps, mutate: mutateOpps } = useSWR<OpportunitiesResult>(
    'motor:opportunities',
    getOpportunities,
    { revalidateOnFocus: false, refreshInterval: 10_000 },
  );

  const { data: xsec } = useSWR<XSecStatus>(
    'motor:xsec',
    getXSecStatus,
    { revalidateOnFocus: false, refreshInterval: 30_000 },
  );

  const [busy, setBusy] = useState<string | null>(null);
  const [confirmKill, setConfirmKill] = useState<string | null>(null);
  const [confirmLive, setConfirmLive] = useState<string | null>(null);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  async function doKill(pair: string) {
    if (!canWrite || busy) return;
    setBusy(`kill:${pair}`);
    setMsg(null);
    setConfirmKill(null);
    try {
      await killBot(pair);
      setMsg({ kind: 'ok', text: t('msg_killed', { pair }) });
      await mutate();
    } catch (e) {
      setMsg({ kind: 'err', text: errText(e, t) });
    } finally {
      setBusy(null);
    }
  }

  async function doTogglePaper(bot: BotInfo) {
    if (!canWrite || busy) return;
    setBusy(`paper:${bot.pair}`);
    setMsg(null);
    setConfirmLive(null);
    try {
      await setBotPaperMode(bot.pair, !bot.paper_mode);
      setMsg({ kind: 'ok', text: t('msg_bot_mode', { pair: bot.pair }) });
      await mutate();
    } catch (e) {
      setMsg({ kind: 'err', text: errText(e, t) });
    } finally {
      setBusy(null);
    }
  }

  const rows = bots?.bots ?? [];
  const th: React.CSSProperties = { textAlign: 'left', padding: '6px 8px', fontSize: 10, color: 'var(--tx-faint)', textTransform: 'uppercase', letterSpacing: '.04em', borderBottom: '1px solid var(--line)' };
  const td: React.CSSProperties = { padding: '8px', fontSize: 12, color: 'var(--tx-hi)', borderBottom: '1px solid var(--line)' };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
      {/* Bots table */}
      <div>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 8 }}>
          <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', textTransform: 'uppercase', letterSpacing: '.06em' }}>
            {t('bots_label')} ({bots?.total ?? 0})
          </div>
          <button type="button" onClick={() => mutate()} style={{ background: 'none', border: 'none', color: 'var(--tx-dim)', fontSize: 11, cursor: 'pointer' }}>
            {t('refresh')}
          </button>
        </div>

        {managerUnavailable ? (
          <ManagerUnavailableNotice />
        ) : isLoading ? (
          <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)', padding: 12 }}>{t('loading')}</div>
        ) : rows.length === 0 ? (
          <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)', padding: 12 }}>{t('no_bots')}</div>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table className="mono" style={{ width: '100%', borderCollapse: 'collapse' }}>
              <thead>
                <tr>
                  <th style={th}>{t('col_pair')}</th>
                  <th style={th}>{t('col_status')}</th>
                  <th style={th}>{t('col_mode')}</th>
                  <th style={th}>{t('col_trades')}</th>
                  <th style={th}>{t('col_pnl')}</th>
                  <th style={th}>{t('col_positions')}</th>
                  <th style={th}>{t('col_actions')}</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((b) => (
                  <tr key={b.bot_id || b.pair}>
                    <td style={td}>{b.pair || (b.pairs?.join(', ') ?? '—')}</td>
                    <td style={td}>
                      <Badge variant={b.status === 'running' ? 'ok' : 'neutral'}>{b.status || '—'}</Badge>
                    </td>
                    <td style={td}>
                      <button
                        type="button"
                        disabled={!canWrite || busy != null} suppressHydrationWarning
                        onClick={() => {
                          // Switching a bot to LIVE uses real money: confirm first.
                          // LIVE -> PAPER is the safe direction and stays direct.
                          if (b.paper_mode) setConfirmLive(b.pair);
                          else doTogglePaper(b);
                        }}
                        title={t('toggle_mode_hint')}
                        style={{
                          background: 'none', border: '1px solid var(--line)', borderRadius: 4,
                          padding: '3px 8px', cursor: canWrite && busy == null ? 'pointer' : 'not-allowed',
                          color: b.paper_mode ? 'var(--brand)' : 'var(--down)', fontSize: 11, fontWeight: 600,
                        }}
                      >
                        {busy === `paper:${b.pair}` ? '…' : b.paper_mode ? t('mode_paper') : t('mode_live')}
                      </button>
                      {confirmLive === b.pair && b.paper_mode && (
                        <div style={{ marginTop: 6, minWidth: 240 }}>
                          <ConfirmInline
                            message={tc('bot_live', { pair: b.pair })}
                            confirmLabel={tc('bot_live_btn')}
                            cancelLabel={tc('cancel')}
                            busy={busy != null}
                            onCancel={() => setConfirmLive(null)}
                            onConfirm={() => doTogglePaper(b)}
                          />
                        </div>
                      )}
                    </td>
                    <td style={td}>{b.trades_today} / {b.total_trades}</td>
                    <td style={{ ...td, color: b.pnl_today >= 0 ? 'var(--up)' : 'var(--down)' }}>
                      {b.pnl_today >= 0 ? '+' : ''}{b.pnl_today.toFixed(2)}
                    </td>
                    <td style={td}>{b.active_positions}</td>
                    <td style={td}>
                      {confirmKill === b.pair ? (
                        <span style={{ display: 'inline-flex', gap: 6 }}>
                          <button
                            type="button"
                            disabled={busy != null} suppressHydrationWarning
                            onClick={() => doKill(b.pair)}
                            style={{ background: 'var(--down)', border: 'none', borderRadius: 4, padding: '3px 10px', color: '#fff', fontSize: 11, fontWeight: 700, cursor: 'pointer' }}
                          >
                            {busy === `kill:${b.pair}` ? '…' : t('confirm')}
                          </button>
                          <button
                            type="button"
                            onClick={() => setConfirmKill(null)}
                            style={{ background: 'none', border: '1px solid var(--line)', borderRadius: 4, padding: '3px 10px', color: 'var(--tx-dim)', fontSize: 11, cursor: 'pointer' }}
                          >
                            {t('cancel')}
                          </button>
                        </span>
                      ) : (
                        <button
                          type="button"
                          disabled={!canWrite || busy != null} suppressHydrationWarning
                          onClick={() => setConfirmKill(b.pair)}
                          style={{
                            background: 'none', border: '1px solid var(--down)', borderRadius: 4,
                            padding: '3px 10px', color: 'var(--down)', fontSize: 11, fontWeight: 600,
                            cursor: canWrite && busy == null ? 'pointer' : 'not-allowed',
                          }}
                        >
                          {t('kill')}
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

      {/* Opportunities */}
      <div>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 8 }}>
          <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', textTransform: 'uppercase', letterSpacing: '.06em' }}>
            {t('opps_label')} ({opps?.total ?? 0})
          </div>
          <button type="button" onClick={() => mutateOpps()} style={{ background: 'none', border: 'none', color: 'var(--tx-dim)', fontSize: 11, cursor: 'pointer' }}>
            {t('refresh')}
          </button>
        </div>
        {(opps?.opportunities?.length ?? 0) === 0 ? (
          <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)', padding: 12 }}>{t('no_opps')}</div>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table className="mono" style={{ width: '100%', borderCollapse: 'collapse' }}>
              <thead>
                <tr>
                  <th style={th}>{t('col_pair')}</th>
                  <th style={th}>{t('col_score')}</th>
                  <th style={th}>{t('col_volatility')}</th>
                  <th style={th}>{t('col_trend')}</th>
                  <th style={th}>{t('col_pf')}</th>
                  <th style={th}>{t('col_reasoning')}</th>
                </tr>
              </thead>
              <tbody>
                {(opps?.opportunities ?? []).map((o, i) => (
                  <tr key={`${o.Pair}-${i}`}>
                    <td style={td}>{o.Pair}</td>
                    <td style={td}>{o.OpportunityScore.toFixed(3)}</td>
                    <td style={td}>{o.Volatility.toFixed(3)}</td>
                    <td style={td}>{o.TrendStrength.toFixed(3)}</td>
                    <td style={td}>{o.ProfitFactor.toFixed(3)}</td>
                    <td style={{ ...td, maxWidth: 280, whiteSpace: 'normal', color: 'var(--tx-dim)' }}>{o.Reasoning || '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {/* XSec status (read-only) */}
      <div>
        <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', textTransform: 'uppercase', letterSpacing: '.06em', marginBottom: 8 }}>
          {t('xsec_label')}
        </div>
        <XSecSummary xsec={xsec} />
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
    </div>
  );
}

function XSecSummary({ xsec }: { xsec?: XSecStatus }) {
  const t = useTranslations('Motor');
  const equity = xsec?.equity_history ?? [];
  const last = equity.length > 0 ? equity[equity.length - 1] : null;
  const positions = xsec?.positions ?? [];

  if (!xsec || (equity.length === 0 && positions.length === 0)) {
    return <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)', padding: 12 }}>{t('no_xsec')}</div>;
  }

  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(140px,1fr))', gap: 10 }}>
      <div style={{ padding: '10px 12px', borderRadius: 'var(--r)', border: '1px solid var(--line)', background: 'var(--bg-inset)' }}>
        <div className="mono" style={{ fontSize: 9, color: 'var(--tx-faint)', textTransform: 'uppercase' }}>{t('xsec_equity')}</div>
        <div style={{ fontSize: 15, fontWeight: 600, color: 'var(--tx-hi)', marginTop: 4 }}>{last ? `$${last.equity.toFixed(2)}` : '—'}</div>
      </div>
      <div style={{ padding: '10px 12px', borderRadius: 'var(--r)', border: '1px solid var(--line)', background: 'var(--bg-inset)' }}>
        <div className="mono" style={{ fontSize: 9, color: 'var(--tx-faint)', textTransform: 'uppercase' }}>{t('xsec_legs')}</div>
        <div style={{ fontSize: 15, fontWeight: 600, color: 'var(--tx-hi)', marginTop: 4 }}>
          {last ? `${last.n_long}L / ${last.n_short}S` : `${positions.length}`}
        </div>
      </div>
      <div style={{ padding: '10px 12px', borderRadius: 'var(--r)', border: '1px solid var(--line)', background: 'var(--bg-inset)' }}>
        <div className="mono" style={{ fontSize: 9, color: 'var(--tx-faint)', textTransform: 'uppercase' }}>{t('xsec_positions')}</div>
        <div style={{ fontSize: 15, fontWeight: 600, color: 'var(--tx-hi)', marginTop: 4 }}>{positions.length}</div>
      </div>
    </div>
  );
}
