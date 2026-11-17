'use client';

// ShadowPanel — control + view for the forward paper/shadow A/B. Shows each
// recorded scorer's realized, net-of-cost, market-neutral paper equity (the
// live "momentum" config vs the "cand_lb14k8" candidate vs "ltr"), and exposes
// every control the A/B needs WITHOUT the CLI/cron:
//   - activate/deactivate candidate recording (xsec.cand_shadow param)
//   - configure the candidate lookback/k (xsec.cand_lookback / xsec.cand_k)
//   - "record now" (enqueue a shadow-record job = xsec-bot -shadow-only)
//   - "reset" a scorer's records to restart the A/B from a fresh t0
// Writes are gated by canWrite (backend permission truth), same as the rest of
// the /xsec panel.

import { useMemo, useState } from 'react';
import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { MultiEquityChart, type MultiEquitySeries } from '@/components/xsec/MultiEquityChart';
import {
  getXsecShadow,
  getXsecParams,
  putXsecParam,
  createXsecJob,
  resetXsecShadow,
  type ShadowEquity,
} from '@/lib/xsec-api';

const SERIES_COLORS: Record<string, string> = {
  momentum: 'var(--tx-dim)',
  ltr: '#e0a44a',
  // candidate uses the brand accent so it stands out in the overlay
};
const CAND_COLOR = 'var(--brand)';

function pct(x: number): string {
  return `${x >= 0 ? '+' : ''}${(x * 100).toFixed(2)}%`;
}
function isCandidate(scorer: string): boolean {
  return scorer.startsWith('cand_');
}

export function ShadowPanel({ canWrite }: { canWrite: boolean }) {
  const t = useTranslations('Xsec');

  const { data, isLoading, mutate } = useSWR('xsec:shadow', getXsecShadow, {
    revalidateOnFocus: false,
    refreshInterval: 30_000,
  });
  const { data: params, mutate: mutateParams } = useSWR('xsec:params', getXsecParams, {
    revalidateOnFocus: false,
    refreshInterval: 60_000,
  });

  const paramVal = (key: string): string | undefined => params?.find((p) => p.key === key)?.value;
  const candOn = (paramVal('xsec.cand_shadow') ?? 'true') !== 'false';
  const candLb = paramVal('xsec.cand_lookback') ?? '14';
  const candK = paramVal('xsec.cand_k') ?? '8';

  const [busy, setBusy] = useState<string | null>(null);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  const [lbDraft, setLbDraft] = useState<string | null>(null);
  const [kDraft, setKDraft] = useState<string | null>(null);

  const scorers = useMemo(() => data?.scorers ?? [], [data?.scorers]);
  const series: MultiEquitySeries[] = useMemo(
    () =>
      scorers
        .filter((s) => s.curve.length >= 2)
        .map((s) => ({
          label: s.scorer,
          color: isCandidate(s.scorer) ? CAND_COLOR : SERIES_COLORS[s.scorer] ?? 'var(--tx-faint)',
          points: s.curve.map((p) => ({ ts: p.ts, value: p.equity })),
        })),
    [scorers],
  );

  async function withBusy(tag: string, fn: () => Promise<void>) {
    if (!canWrite || busy) return;
    setBusy(tag);
    setMsg(null);
    try {
      await fn();
    } catch (e) {
      setMsg({ kind: 'err', text: e instanceof Error ? e.message : String(e) });
    } finally {
      setBusy(null);
    }
  }

  const toggleCand = () =>
    withBusy('toggle', async () => {
      await putXsecParam('xsec.cand_shadow', candOn ? 'false' : 'true');
      await mutateParams();
      setMsg({ kind: 'ok', text: t('shadow_toggle_ok') });
    });

  const saveConfig = () =>
    withBusy('config', async () => {
      const lb = lbDraft ?? candLb;
      const k = kDraft ?? candK;
      const lbN = Number(lb);
      const kN = Number(k);
      if (!Number.isInteger(lbN) || lbN < 2 || lbN > 120) throw new Error(t('shadow_lb_invalid'));
      if (!Number.isInteger(kN) || kN < 1 || kN > 20) throw new Error(t('shadow_k_invalid'));
      await putXsecParam('xsec.cand_lookback', String(lbN));
      await putXsecParam('xsec.cand_k', String(kN));
      await mutateParams();
      setLbDraft(null);
      setKDraft(null);
      setMsg({ kind: 'ok', text: t('shadow_config_ok') });
    });

  const recordNow = () =>
    withBusy('record', async () => {
      await createXsecJob({ mode: 'shadow-record' });
      setMsg({ kind: 'ok', text: t('shadow_record_ok') });
      setTimeout(() => void mutate(), 2500);
    });

  const reset = (scorer: string) =>
    withBusy('reset:' + scorer, async () => {
      if (typeof window !== 'undefined' && !window.confirm(t('shadow_reset_confirm', { scorer }))) return;
      const r = await resetXsecShadow(scorer);
      await mutate();
      setMsg({ kind: 'ok', text: t('shadow_reset_ok', { n: r.deleted }) });
    });

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      <p className="mono" style={{ fontSize: 11, color: 'var(--tx-dim)', margin: 0, lineHeight: 1.5 }}>
        {t('shadow_intro')}
      </p>

      {/* Controls */}
      <div
        style={{
          display: 'flex', flexWrap: 'wrap', gap: 10, alignItems: 'flex-end',
          padding: '12px', borderRadius: 'var(--r)', border: '1px solid var(--line)',
          background: 'var(--bg-soft, rgba(255,255,255,.02))',
        }}
      >
        <button
          type="button"
          onClick={toggleCand}
          disabled={!canWrite || busy != null} suppressHydrationWarning
          style={pillStyle(candOn ? 'on' : 'off', canWrite)}
        >
          {candOn ? t('shadow_active') : t('shadow_inactive')}
        </button>

        <LabeledNum label={t('shadow_field_lb')} value={lbDraft ?? candLb} onChange={setLbDraft} disabled={!canWrite} />
        <LabeledNum label={t('shadow_field_k')} value={kDraft ?? candK} onChange={setKDraft} disabled={!canWrite} />
        <button type="button" onClick={saveConfig} disabled={!canWrite || busy != null} suppressHydrationWarning style={actionBtn(canWrite)}>
          {busy === 'config' ? '…' : t('shadow_save_config')}
        </button>

        <div style={{ flex: 1 }} />

        <button type="button" onClick={recordNow} disabled={!canWrite || busy != null} suppressHydrationWarning style={actionBtn(canWrite)}>
          {busy === 'record' ? '…' : t('shadow_record_now')}
        </button>
      </div>

      {msg && (
        <div
          className="mono"
          style={{
            fontSize: 11, padding: '8px 10px', borderRadius: 'var(--r)',
            color: msg.kind === 'ok' ? 'var(--up)' : 'var(--down)',
            background: msg.kind === 'ok' ? 'rgba(74,222,128,.07)' : 'rgba(233,90,90,.07)',
            border: `1px solid ${msg.kind === 'ok' ? 'var(--up)' : 'var(--down)'}`,
          }}
        >
          {msg.text}
        </div>
      )}

      {/* Comparison curves */}
      {series.length > 0 && (
        <div>
          <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', marginBottom: 6 }}>
            {t('shadow_curve_caption')}
          </div>
          <MultiEquityChart series={series} />
        </div>
      )}

      {/* Per-scorer summary */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
        {isLoading && <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}>…</div>}
        {!isLoading && scorers.length === 0 && (
          <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{t('shadow_empty')}</div>
        )}
        {scorers.map((s) => (
          <ScorerRow key={s.scorer} s={s} canWrite={canWrite} busy={busy} onReset={() => reset(s.scorer)} t={t} />
        ))}
      </div>
    </div>
  );
}

function ScorerRow({
  s, canWrite, busy, onReset, t,
}: {
  s: ShadowEquity;
  canWrite: boolean;
  busy: string | null;
  onReset: () => void;
  t: ReturnType<typeof useTranslations>;
}) {
  const cand = isCandidate(s.scorer);
  const netPos = s.total_return >= 0;
  return (
    <div
      style={{
        display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap',
        padding: '10px 12px', borderRadius: 'var(--r)',
        border: `1px solid ${cand ? 'var(--brand)' : 'var(--line)'}`,
        background: cand ? 'rgba(157,124,255,.06)' : 'transparent',
      }}
    >
      <div style={{ minWidth: 110 }}>
        <div className="mono" style={{ fontSize: 12, color: cand ? 'var(--brand)' : 'var(--tx-hi)', fontWeight: 700 }}>
          {s.scorer}
        </div>
        <div className="mono" style={{ fontSize: 9, color: 'var(--tx-faint)' }}>
          {cand ? t('shadow_tag_candidate') : s.scorer === 'momentum' ? t('shadow_tag_live') : t('shadow_tag_other')}
        </div>
      </div>
      <Metric label={t('shadow_m_periods')} value={String(s.n_periods)} />
      <Metric label={t('shadow_m_net')} value={pct(s.total_return)} color={netPos ? 'var(--up)' : 'var(--down)'} />
      <Metric label={t('shadow_m_gross')} value={pct(s.gross_return)} />
      <Metric label={t('shadow_m_avg')} value={pct(s.avg_period_ret)} />
      <div style={{ flex: 1 }} />
      {canWrite && (
        <button
          type="button"
          onClick={onReset}
          disabled={busy != null || s.n_periods === 0} suppressHydrationWarning
          style={{
            fontSize: 10, padding: '4px 8px', borderRadius: 'var(--r)', cursor: 'pointer',
            background: 'none', border: '1px solid var(--down)', color: 'var(--down)',
            opacity: busy != null || s.n_periods === 0 ? 0.4 : 1,
          }}
        >
          {busy === 'reset:' + s.scorer ? '…' : t('shadow_reset')}
        </button>
      )}
    </div>
  );
}

function Metric({ label, value, color }: { label: string; value: string; color?: string }) {
  return (
    <div style={{ minWidth: 64 }}>
      <div className="mono" style={{ fontSize: 9, color: 'var(--tx-faint)', textTransform: 'uppercase', letterSpacing: '.04em' }}>
        {label}
      </div>
      <div className="mono" style={{ fontSize: 13, color: color ?? 'var(--tx-hi)', fontWeight: 600 }}>{value}</div>
    </div>
  );
}

function LabeledNum({
  label, value, onChange, disabled,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  disabled: boolean;
}) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
      <span className="mono" style={{ fontSize: 9, color: 'var(--tx-faint)', textTransform: 'uppercase' }}>{label}</span>
      <input
        type="number"
        value={value}
        disabled={disabled} suppressHydrationWarning
        onChange={(e) => onChange(e.target.value)}
        style={{
          width: 60, padding: '5px 7px', borderRadius: 'var(--r)', fontSize: 12,
          background: 'var(--bg)', border: '1px solid var(--line)', color: 'var(--tx-hi)',
        }}
      />
    </label>
  );
}

function pillStyle(state: 'on' | 'off', canWrite: boolean): React.CSSProperties {
  const on = state === 'on';
  return {
    padding: '7px 14px', borderRadius: 'var(--r)', fontSize: 12, fontWeight: 600,
    cursor: canWrite ? 'pointer' : 'not-allowed',
    border: `1px solid ${on ? 'var(--up)' : 'var(--line)'}`,
    background: on ? 'rgba(74,222,128,.1)' : 'transparent',
    color: on ? 'var(--up)' : 'var(--tx-dim)',
    opacity: canWrite ? 1 : 0.6,
  };
}
function actionBtn(canWrite: boolean): React.CSSProperties {
  return {
    padding: '7px 14px', borderRadius: 'var(--r)', fontSize: 12, fontWeight: 600,
    cursor: canWrite ? 'pointer' : 'not-allowed',
    border: '1px solid var(--brand)', background: 'rgba(157,124,255,.1)', color: 'var(--brand)',
    opacity: canWrite ? 1 : 0.5,
  };
}
