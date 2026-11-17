'use client';

import { useState } from 'react';
import { useTranslations } from 'next-intl';
import { Panel, PanelHeader, PanelBody } from '@/components/ui/Panel';
import { Badge } from '@/components/ui/Badge';
import {
  launchErrorInfo, launchErrorKey, launchKillSwitch, launchResume, verdictTone,
  type GateEvaluation, type LaunchStatus,
} from '@/lib/launch-api';
import {
  Empty, Kpi, btnStyle, fmtNum, fmtTime, fmtUsd, inputStyle, pnlColor, tBand, tReason,
  tdRight, tdStyle, thRight, thStyle, theadRow, useOperatePermission,
} from '@/components/launch/shared';

export function GateVerdictCard({ gate }: { gate: GateEvaluation | null }) {
  const t = useTranslations('Launch');
  if (!gate) {
    return (
      <Panel>
        <PanelHeader title={t('gate_title')} />
        <PanelBody><Empty text={t('gate_none')} /></PanelBody>
      </Panel>
    );
  }
  const r = gate.requirements;
  return (
    <Panel>
      <PanelHeader title={t('gate_title')} sub={t('gate_evaluated_at', { time: fmtTime(gate.evaluated_at) })}>
        <Badge variant={verdictTone(gate.verdict)} style={{ fontSize: 14, padding: '6px 14px' }}>
          {t(`verdict_${gate.verdict}` as never)}
        </Badge>
      </PanelHeader>
      <PanelBody>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 20, marginBottom: 12 }}>
          <Kpi label={t('m_trades')} value={String(gate.trades)} />
          <Kpi label={t('m_weeks')} value={fmtNum(gate.shadow_weeks, 1)} />
          <Kpi label={t('m_net_pnl')} value={fmtUsd(gate.net_pnl_usd)} color={pnlColor(gate.net_pnl_usd)} />
          <Kpi label={t('m_ci', { level: Math.round(gate.ci_level * 100) })} value={`${fmtUsd(gate.ci_low)} … ${fmtUsd(gate.ci_high)}`} />
          <Kpi label={t('m_pf')} value={fmtNum(gate.profit_factor)} />
          <Kpi label={t('m_fraud')} value={String(gate.fraud_buys)} color={gate.fraud_buys > 0 ? 'var(--down)' : undefined} />
          <Kpi label={t('m_mean')} value={fmtUsd(gate.mean_pnl_usd)} color={pnlColor(gate.mean_pnl_usd)} />
          <Kpi label={t('m_drawdown')} value={fmtUsd(gate.max_drawdown_usd)} />
        </div>
        <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', textTransform: 'uppercase' }}>{t('requirements')}</div>
        <ul style={{ margin: '4px 0 10px', paddingLeft: 18, fontSize: 12, color: 'var(--tx-dim)' }}>
          <li>{t('req_trades', { n: r.min_trades })}</li>
          <li>{t('req_weeks', { n: r.min_shadow_weeks })}</li>
          <li>{t('req_fraud', { n: r.max_fraud_buys })}</li>
        </ul>
        {gate.detail && (
          <div style={{ fontSize: 12, color: 'var(--tx-dim)' }}>
            <span className="mono" style={{ color: 'var(--tx-faint)', fontSize: 10, textTransform: 'uppercase' }}>{t('gate_detail')}: </span>
            {gate.detail}
          </div>
        )}
      </PanelBody>
    </Panel>
  );
}

function ControlPanel({ status, onDone }: { status: LaunchStatus; onDone: () => void }) {
  const t = useTranslations('Launch');
  const perm = useOperatePermission();
  const killed = status.breaker?.state === 'killed';
  const [mode, setMode] = useState<'kill' | 'resume' | null>(null);
  const [reason, setReason] = useState('');
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);

  const reasonOk = reason.trim().length > 0;
  const canSend = perm.allowed && reasonOk && confirmed && !busy && mode != null;

  async function send() {
    if (!mode) return;
    setBusy(true);
    setMsg(null);
    try {
      const res = mode === 'kill' ? await launchKillSwitch(reason.trim()) : await launchResume(reason.trim());
      setMsg({ ok: true, text: t('ctrl_queued', { id: res.command_id }) });
      setMode(null);
      setReason('');
      setConfirmed(false);
      onDone();
    } catch (e) {
      setMsg({ ok: false, text: t(launchErrorKey(launchErrorInfo(e)) as never) });
    } finally {
      setBusy(false);
    }
  }

  const open = (m: 'kill' | 'resume') => { setMode(m); setConfirmed(false); setMsg(null); };
  const btnDisabled = !perm.allowed;

  return (
    <Panel>
      <PanelHeader title={t('ctrl_title')} />
      <PanelBody>
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center' }}>
          <button type="button" disabled={btnDisabled} suppressHydrationWarning onClick={() => open('kill')} style={btnStyle('down', btnDisabled)}>
            {t('ctrl_kill')}
          </button>
          <button type="button" disabled={btnDisabled} suppressHydrationWarning onClick={() => open('resume')} style={btnStyle('neutral', btnDisabled)}>
            {t('ctrl_resume')}
          </button>
          {btnDisabled && (
            <span className="mono" suppressHydrationWarning style={{ fontSize: 11, color: 'var(--tx-faint)' }}>
              {perm.ready ? t('no_permission') : t('perm_loading')}
            </span>
          )}
          {killed && <Badge variant="real">{t('breaker_killed')}</Badge>}
        </div>
        {mode && (
          <div style={{ marginTop: 12, padding: 12, border: `1px solid ${mode === 'kill' ? 'var(--down)' : 'var(--line)'}`, borderRadius: 'var(--r)', display: 'grid', gap: 8 }}>
            <label style={{ fontSize: 11, color: 'var(--tx-dim)' }}>{t('ctrl_reason_label')}</label>
            <input value={reason} onChange={(e) => setReason(e.target.value)} placeholder={t('ctrl_reason_ph')} style={inputStyle} />
            <label style={{ fontSize: 12, color: mode === 'kill' ? 'var(--down)' : 'var(--tx-hi)', display: 'flex', gap: 8, alignItems: 'center' }}>
              <input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} />
              {mode === 'kill' ? t('ctrl_confirm_kill') : t('ctrl_confirm_resume')}
            </label>
            <div style={{ display: 'flex', gap: 8 }}>
              <button type="button" disabled={!canSend} onClick={send} style={btnStyle(mode === 'kill' ? 'down' : 'brand', !canSend)}>
                {busy ? t('working') : t('ctrl_send')}
              </button>
              <button type="button" onClick={() => setMode(null)} style={btnStyle('neutral', false)}>{t('ctrl_cancel')}</button>
            </div>
          </div>
        )}
        {msg && <div style={{ marginTop: 10, fontSize: 12, color: msg.ok ? 'var(--up)' : 'var(--down)' }}>{msg.text}</div>}
      </PanelBody>
    </Panel>
  );
}

function CountList({ title, data, label }: { title: string; data: Record<string, number>; label: (k: string) => string }) {
  const entries = Object.entries(data).sort((a, b) => b[1] - a[1]);
  return (
    <div style={{ flex: 1, minWidth: 220 }}>
      <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', textTransform: 'uppercase', marginBottom: 4 }}>{title}</div>
      {entries.length === 0 ? <Empty text="—" /> : (
        <table style={{ width: '100%', fontSize: 12, borderCollapse: 'collapse' }}>
          <tbody>
            {entries.map(([k, v]) => (
              <tr key={k}><td style={tdStyle}>{label(k)}</td><td style={tdRight} className="mono">{v}</td></tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

export function LaunchSummary({ status, onChanged }: { status: LaunchStatus; onChanged: () => void }) {
  const t = useTranslations('Launch');
  const killed = status.breaker?.state === 'killed';
  const sh = status.shadow;
  const c = status.counts_24h;
  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0, 1fr)', gap: 16 }}>
      <GateVerdictCard gate={status.gate} />

      <Panel>
        <PanelHeader title={t('sources_title')} sub={`${t('service_' + status.service as never)} · ${t('last_heartbeat')}: ${fmtTime(status.last_heartbeat)}`} />
        <PanelBody>
          {status.sources.length === 0 ? <Empty text={t('sources_empty')} /> : (
            <div style={{ overflowX: 'auto' }}>
              <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
                <thead>
                  <tr style={theadRow}>
                    <th style={thStyle}>{t('col_source')}</th><th style={thStyle}>{t('col_role')}</th>
                    <th style={thStyle}>{t('col_connected')}</th><th style={thStyle}>{t('col_active')}</th>
                    <th style={thStyle}>{t('col_last_event')}</th><th style={thRight}>{t('col_events_min')}</th>
                    <th style={thRight}>{t('col_rate_limited')}</th><th style={thRight}>{t('col_quota')}</th>
                  </tr>
                </thead>
                <tbody>
                  {status.sources.map((s) => (
                    <tr key={s.name}>
                      <td style={tdStyle} className="mono">{s.name}</td>
                      <td style={tdStyle}>{s.role}</td>
                      <td style={tdStyle}><Badge variant={s.connected ? 'ok' : 'real'}>{s.connected ? t('yes') : t('no')}</Badge></td>
                      <td style={tdStyle}><Badge variant={s.active ? 'ok' : 'neutral'}>{s.active ? t('yes') : t('no')}</Badge></td>
                      <td style={tdStyle}>{fmtTime(s.last_event_at)}</td>
                      <td style={tdRight} className="mono">{fmtNum(s.events_per_min, 1)}</td>
                      <td style={tdRight} className="mono">{s.rate_limited}</td>
                      <td style={tdRight} className="mono">{s.quota_used_pct == null ? '—' : `${fmtNum(s.quota_used_pct, 1)}%`}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </PanelBody>
      </Panel>

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(280px, 1fr))', gap: 16 }}>
        <Panel>
          <PanelHeader title={t('breaker_title')} />
          <PanelBody>
            <Badge variant={killed ? 'real' : 'ok'}>{killed ? t('breaker_killed') : t('breaker_running')}</Badge>
            {killed && status.breaker && (
              <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 6 }}>
                {status.breaker.since && t('breaker_since', { time: fmtTime(status.breaker.since) })}{' '}
                {status.breaker.actor && t('breaker_by', { actor: status.breaker.actor })}
                {status.breaker.reason && <div>{status.breaker.reason}</div>}
              </div>
            )}
            {status.entries_paused && (
              <div style={{ fontSize: 12, color: 'var(--warn)', marginTop: 6 }}>
                {t('entries_paused', { reason: status.pause_reason ? tReason(t, status.pause_reason) : '—' })}
              </div>
            )}
            <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', textTransform: 'uppercase', margin: '12px 0 4px' }}>{t('network_title')}</div>
            <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
              {Object.entries(status.network_state ?? {}).map(([net, st]) => (
                <Badge key={net} variant={st === 'OK' ? 'ok' : 'micro'}>
                  {net}: {st === 'OK' || st === 'PAUSE_ENTRIES' ? t(`network_${st}` as never) : st}
                </Badge>
              ))}
            </div>
          </PanelBody>
        </Panel>

        <Panel>
          <PanelHeader title={t('shadow_title')} />
          <PanelBody>
            {sh ? (
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 18 }}>
                <Kpi label={t('s_open')} value={String(sh.open_positions)} />
                <Kpi label={t('s_exposure')} value={fmtUsd(sh.exposure_usd)} />
                <Kpi label={t('s_closed')} value={String(sh.closed_positions)} />
                <Kpi label={t('s_net')} value={fmtUsd(sh.net_pnl_usd)} color={pnlColor(sh.net_pnl_usd)} />
                <Kpi label={t('s_gross')} value={fmtUsd(sh.gross_pnl_usd)} color={pnlColor(sh.gross_pnl_usd)} />
              </div>
            ) : <Empty text={t('empty')} />}
          </PanelBody>
        </Panel>
      </div>

      <Panel>
        <PanelHeader title={t('counts_title')} />
        <PanelBody>
          {c ? (
            <>
              <div style={{ display: 'flex', gap: 24, marginBottom: 12 }}>
                <Kpi label={t('c_events')} value={String(c.events)} />
                <Kpi label={t('c_opportunities')} value={String(c.opportunities)} />
                <Kpi label={t('c_rejected')} value={String(c.rejected)} />
              </div>
              <div style={{ display: 'flex', gap: 24, flexWrap: 'wrap' }}>
                <CountList title={t('by_reason')} data={c.by_reason ?? {}} label={(k) => tReason(t, k)} />
                <CountList title={t('by_band')} data={c.by_band ?? {}} label={(k) => tBand(t, k)} />
              </div>
            </>
          ) : <Empty text={t('empty')} />}
        </PanelBody>
      </Panel>

      <ControlPanel status={status} onDone={onChanged} />
    </div>
  );
}
