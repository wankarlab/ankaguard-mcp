'use client';

// MasterControls — start/stop/tick the master agent, toggle the global spawner
// paper-mode, and (Level-4) emergency-stop every active bot. Status is polled via
// SWR. All thresholds (spawn threshold etc.) are read-only from the backend
// snapshot — this surface never hardcodes them.

import { useState } from 'react';
import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { Badge } from '@/components/ui/Badge';
import { ConfirmInline } from '@/components/ui/ConfirmInline';
import { ApiError } from '@/lib/api';
import { isServiceUnavailable } from '@/lib/motor-availability';
import { ManagerUnavailableNotice } from './ManagerUnavailableNotice';
import {
  getMasterStatus,
  startMaster,
  stopMaster,
  forceTick,
  getMasterPaperMode,
  setMasterPaperMode,
  emergencyStopAll,
  type MasterState,
  type PaperModeState,
} from '@/lib/motor-api';

function errText(e: unknown, t: ReturnType<typeof useTranslations>): string {
  if (e instanceof ApiError) {
    if (e.status === 403) return `403 · ${t('err_403')}`;
    if (e.status === 401) return `401 · ${t('err_401')}`;
    if (e.status === 503) return `503 · ${t('err_503')}`;
    if (e.status === 409) {
      const body = e.body as { error?: string } | undefined;
      return `409 · ${body?.error ?? t('err_conflict')}`;
    }
    const body = e.body as { error?: string; message?: string } | undefined;
    const detail = body?.message || body?.error || '';
    return `${e.status}${detail ? ` · ${detail}` : ''}`;
  }
  return t('action_error');
}

interface Props {
  canWrite: boolean;
}

export function MasterControls({ canWrite }: Props) {
  const t = useTranslations('Motor');
  const tc = useTranslations('Confirm');

  const { data: state, isLoading, mutate } = useSWR<MasterState>(
    'motor:master-status',
    getMasterStatus,
    { revalidateOnFocus: false, refreshInterval: 5_000 },
  );

  // Global spawner paper-mode only exists when the bot manager is wired; the
  // endpoint answers 503 otherwise. Detect it by HTTP status and swap the switch
  // for a notice (master agent, opportunities and emergency stop stay usable).
  const { data: paper, error: paperError, mutate: mutatePaper } = useSWR<PaperModeState>(
    'motor:master-paper-mode',
    getMasterPaperMode,
    { revalidateOnFocus: false, refreshInterval: 15_000, shouldRetryOnError: false },
  );
  const paperUnavailable = isServiceUnavailable(paperError);

  const [busy, setBusy] = useState<string | null>(null);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  const [confirmStop, setConfirmStop] = useState(false);
  const [confirmLive, setConfirmLive] = useState(false);

  async function run(action: string, fn: () => Promise<unknown>, okMsg: string) {
    if (!canWrite || busy) return;
    setBusy(action);
    setMsg(null);
    try {
      await fn();
      setMsg({ kind: 'ok', text: okMsg });
      await Promise.all([mutate(), mutatePaper()]);
    } catch (e) {
      setMsg({ kind: 'err', text: errText(e, t) });
    } finally {
      setBusy(null);
    }
  }

  const running = !!state?.Running;
  const paperOn = !!paper?.paper_mode;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {/* Status header */}
      <div
        style={{
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
          flexWrap: 'wrap', gap: 12, padding: 14, borderRadius: 'var(--r)',
          border: `1px solid ${running ? 'var(--up)' : 'var(--line)'}`,
          background: running ? 'rgba(74,219,132,.05)' : 'var(--bg-inset)',
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          <span
            style={{
              width: 10, height: 10, borderRadius: '50%',
              background: running ? 'var(--up)' : 'var(--tx-faint)',
              boxShadow: running ? '0 0 8px var(--up)' : 'none',
            }}
          />
          <div>
            <div style={{ fontSize: 14, fontWeight: 600, color: 'var(--tx-hi)' }}>
              {isLoading ? t('loading') : running ? t('status_running') : t('status_stopped')}
            </div>
            <div className="mono" style={{ fontSize: 10, color: 'var(--tx-dim)', marginTop: 2 }}>
              {t('scan_count')}: {state?.ScanCount ?? 0} · {t('active_bots')}: {state?.ActiveBots ?? 0}
            </div>
          </div>
        </div>
        {paper && !paperUnavailable && (
          <Badge variant={paperOn ? 'paper' : 'real'}>{paperOn ? t('mode_paper') : t('mode_live')}</Badge>
        )}
      </div>

      {/* Metrics grid */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(140px,1fr))', gap: 10 }}>
        <Metric label={t('m_capital')} value={`$${(state?.TotalCapitalUSD ?? 0).toFixed(2)}`} />
        <Metric label={t('m_allocated')} value={`$${(state?.AllocatedUSD ?? 0).toFixed(2)}`} />
        <Metric label={t('m_spawn_threshold')} value={(state?.SpawnThreshold ?? 0).toFixed(2)} />
        <Metric label={t('m_multipair')} value={state?.MultiPairEnabled ? t('on') : t('off')} />
        <Metric label={t('m_last_scan')} value={fmtTime(state?.LastScanAt)} />
        <Metric label={t('m_opportunities')} value={`${state?.Opportunities?.length ?? 0}`} />
      </div>

      {state?.LastError && (
        <div className="mono" style={{ fontSize: 11, color: 'var(--down)', padding: '8px 12px', borderRadius: 'var(--r)', background: 'rgba(233,90,90,.07)', border: '1px solid var(--down)' }}>
          {t('last_error')}: {state.LastError}
        </div>
      )}

      {/* Action buttons */}
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
        <ActionButton
          label={running ? t('btn_running') : t('btn_start')}
          variant="up"
          disabled={!canWrite || running || busy != null}
          loading={busy === 'start'}
          onClick={() => run('start', startMaster, t('msg_started'))}
        />
        <ActionButton
          label={t('btn_stop')}
          variant="down"
          disabled={!canWrite || !running || busy != null}
          loading={busy === 'stop'}
          onClick={() => run('stop', stopMaster, t('msg_stopped'))}
        />
        <ActionButton
          label={t('btn_tick')}
          variant="neutral"
          disabled={!canWrite || busy != null}
          loading={busy === 'tick'}
          onClick={() => run('tick', forceTick, t('msg_ticked'))}
        />
      </div>

      {/* Global paper-mode toggle */}
      {paperUnavailable ? (
        <ManagerUnavailableNotice />
      ) : (
      <div
        style={{
          display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12,
          flexWrap: 'wrap', padding: 12, borderRadius: 'var(--r)', border: '1px solid var(--line)',
        }}
      >
        <div>
          <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--tx-hi)' }}>{t('paper_toggle_title')}</div>
          <div style={{ fontSize: 11, color: 'var(--tx-dim)', marginTop: 2 }}>
            {t('paper_toggle_sub')}
          </div>
        </div>
        <button
            type="button"
            role="switch"
            aria-checked={paperOn}
            disabled={!canWrite || busy != null} suppressHydrationWarning
            onClick={() => {
              // PAPER -> LIVE moves real money: require explicit confirmation.
              // LIVE -> PAPER is the safe direction and stays direct.
              if (paperOn) setConfirmLive(true);
              else run('paper', () => setMasterPaperMode(true), t('msg_paper_on'));
            }}
            style={{
              width: 56, height: 30, borderRadius: 16, position: 'relative',
              border: '1px solid var(--line)', cursor: canWrite && busy == null ? 'pointer' : 'not-allowed',
              background: paperOn ? 'var(--brand)' : 'var(--down)', transition: 'background .15s',
            }}
          >
            <span style={{ position: 'absolute', top: 3, left: paperOn ? 28 : 3, width: 22, height: 22, borderRadius: '50%', background: 'var(--tx-hi)', transition: 'left .15s' }} />
        </button>
        {confirmLive && paperOn && (
          <div style={{ flexBasis: '100%' }}>
            <ConfirmInline
              message={tc('paper_to_live')}
              confirmLabel={busy === 'paper' ? t('working') : tc('paper_to_live_btn')}
              cancelLabel={tc('cancel')}
              busy={busy != null}
              onCancel={() => setConfirmLive(false)}
              onConfirm={async () => {
                setConfirmLive(false);
                await run('paper', () => setMasterPaperMode(false), t('msg_live_on'));
              }}
            />
          </div>
        )}
      </div>
      )}

      {/* Emergency stop-all (Level 4) */}
      <div
        style={{
          padding: 14, borderRadius: 'var(--r)', border: '1px solid var(--down)',
          background: 'rgba(233,90,90,.05)',
        }}
      >
        <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--down)' }}>{t('emergency_title')}</div>
        <div style={{ fontSize: 11, color: 'var(--tx-dim)', marginTop: 2, marginBottom: 12 }}>{t('emergency_sub')}</div>
        {!confirmStop ? (
          <button
            type="button"
            disabled={!canWrite || busy != null} suppressHydrationWarning
            onClick={() => setConfirmStop(true)}
            style={emergencyBtn(!canWrite || busy != null)}
          >
            {t('emergency_btn')}
          </button>
        ) : (
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
            <span style={{ fontSize: 12, color: 'var(--down)', fontWeight: 600 }}>{t('emergency_confirm')}</span>
            <button
              type="button"
              disabled={busy != null} suppressHydrationWarning
              onClick={async () => {
                setConfirmStop(false);
                await run('emergency', () => emergencyStopAll({ dryRun: false, reason: 'motor emergency-stop-all' }), t('msg_emergency'));
              }}
              style={emergencyBtn(busy != null)}
            >
              {busy === 'emergency' ? t('working') : t('emergency_confirm_btn')}
            </button>
            <button
              type="button"
              onClick={() => setConfirmStop(false)}
              style={{ padding: '8px 14px', borderRadius: 'var(--r)', border: '1px solid var(--line)', background: 'transparent', color: 'var(--tx-dim)', fontSize: 12, cursor: 'pointer' }}
            >
              {t('cancel')}
            </button>
          </div>
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
    </div>
  );
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div style={{ padding: '10px 12px', borderRadius: 'var(--r)', border: '1px solid var(--line)', background: 'var(--bg-inset)' }}>
      <div className="mono" style={{ fontSize: 9, color: 'var(--tx-faint)', textTransform: 'uppercase', letterSpacing: '.05em' }}>{label}</div>
      <div style={{ fontSize: 15, fontWeight: 600, color: 'var(--tx-hi)', marginTop: 4 }}>{value}</div>
    </div>
  );
}

function ActionButton({ label, variant, disabled, loading, onClick }: {
  label: string;
  variant: 'up' | 'down' | 'neutral';
  disabled: boolean;
  loading: boolean;
  onClick: () => void;
}) {
  const bg = variant === 'up' ? 'var(--up)' : variant === 'down' ? 'var(--down)' : 'var(--brand)';
  return (
    <button
      type="button"
      disabled={disabled} suppressHydrationWarning
      onClick={onClick}
      style={{
        padding: '9px 18px', borderRadius: 'var(--r)', border: 'none',
        background: disabled ? 'var(--bg-void)' : bg,
        color: disabled ? 'var(--tx-faint)' : 'var(--bg)',
        fontSize: 13, fontWeight: 600, cursor: disabled ? 'not-allowed' : 'pointer',
        opacity: loading ? 0.7 : 1,
      }}
    >
      {loading ? '…' : label}
    </button>
  );
}

function emergencyBtn(disabled: boolean): React.CSSProperties {
  return {
    padding: '9px 18px', borderRadius: 'var(--r)', border: 'none',
    background: disabled ? 'var(--bg-void)' : 'var(--down)',
    color: disabled ? 'var(--tx-faint)' : '#fff',
    fontSize: 13, fontWeight: 700, cursor: disabled ? 'not-allowed' : 'pointer',
  };
}

function fmtTime(iso?: string): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (isNaN(d.getTime()) || d.getUTCFullYear() < 2000) return '—';
  return d.toLocaleTimeString();
}
