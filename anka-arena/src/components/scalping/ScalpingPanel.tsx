'use client';

import { useMemo, useState } from 'react';
import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { Badge } from '@/components/ui/Badge';
import { ApiError } from '@/lib/api';
import {
  KNOBS,
  PRESETS,
  fetchGateParams,
  setKnob,
  applyPreset,
  deriveBlockers,
  type Knob,
  type ParamItem,
  type ScalpingPreset,
} from '@/lib/scalping';

// Pull the real backend reason from an ApiError: 403 = level, 400 = validation,
// 500 = server. The config write group requires level 3+.
function errInfo(e: unknown): { status: number; detail: string } | null {
  if (e instanceof ApiError) {
    const body = e.body as { error?: string; message?: string } | undefined;
    return { status: e.status, detail: body?.message || body?.error || '' };
  }
  return null;
}

const toneColor: Record<ScalpingPreset['tone'], string> = {
  safe: 'var(--up)',
  neutral: 'var(--brand)',
  hot: 'var(--down)',
};

interface Props {
  /** Backend-truth write gate (from me/permissions). Read-only when false. */
  canWrite: boolean;
}

export function ScalpingPanel({ canWrite }: Props) {
  const t = useTranslations('Scalping');

  const { data, error, isLoading, mutate } = useSWR(
    'scalping:gate-params',
    fetchGateParams,
    { revalidateOnFocus: false, refreshInterval: 15_000 },
  );

  // Pending edits for the advanced sliders: key → string value (only dirty keys).
  const [edits, setEdits] = useState<Record<string, string>>({});
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  // Preset awaiting confirmation (two-step to avoid a fat-finger live change).
  const [confirmPreset, setConfirmPreset] = useState<ScalpingPreset | null>(null);
  const [showAdvanced, setShowAdvanced] = useState(false);

  const params = useMemo(() => data ?? {}, [data]);
  const dirtyKeys = Object.keys(edits);
  const reasonOk = reason.trim().length >= 6;

  const blockers = useMemo(() => deriveBlockers(params), [params]);

  // Effective value for a knob, preferring a pending edit.
  function effVal(key: string): string {
    if (key in edits) return edits[key];
    return params[key]?.current_effective_value ?? params[key]?.default ?? '';
  }

  function editKnob(key: string, value: string) {
    setMsg(null);
    setEdits((prev) => {
      const base = params[key]?.current_effective_value ?? params[key]?.default ?? '';
      const next = { ...prev };
      if (value === base) delete next[key];
      else next[key] = value;
      return next;
    });
  }

  async function commitAdvanced() {
    if (!canWrite || !reasonOk || dirtyKeys.length === 0) return;
    setBusy(true);
    setMsg(null);
    try {
      for (const key of dirtyKeys) {
        await setKnob(key, edits[key], reason.trim());
      }
      setEdits({});
      setReason('');
      setMsg({ kind: 'ok', text: t('saved', { n: dirtyKeys.length }) });
      await mutate();
    } catch (e) {
      const info = errInfo(e);
      const text = !info
        ? t('save_error')
        : info.status === 403 ? `403 · ${t('err_403')}`
        : info.status === 401 ? `401 · ${t('err_401')}`
        : `${info.status}${info.detail ? ` · ${info.detail}` : ''}`;
      setMsg({ kind: 'err', text });
    } finally {
      setBusy(false);
    }
  }

  // One-click fix for the validation gate specifically: it's the blocker most
  // likely to sit ON silently for a long time (it was left ON as a stopgap
  // during an unrelated fix and stayed that way for a full day, unnoticed,
  // because reaching it required opening Advanced). Surfaced directly on the
  // gate-state banner instead of buried in the knob list.
  async function quickUnblockValgate() {
    if (!canWrite || busy) return;
    setBusy(true);
    setMsg(null);
    try {
      await setKnob('bot.validation_gate.enabled', 'false', 'quick-fix: unblock via gate-state banner');
      setMsg({ kind: 'ok', text: t('unblocked') });
      await mutate();
    } catch (e) {
      const info = errInfo(e);
      const text = !info
        ? t('save_error')
        : info.status === 403 ? `403 · ${t('err_403')}`
        : info.status === 401 ? `401 · ${t('err_401')}`
        : `${info.status}${info.detail ? ` · ${info.detail}` : ''}`;
      setMsg({ kind: 'err', text });
    } finally {
      setBusy(false);
    }
  }

  async function doApplyPreset(p: ScalpingPreset) {
    if (!canWrite) return;
    setBusy(true);
    setMsg(null);
    try {
      await applyPreset(p, `preset:${p.id} via scalping panel`);
      setConfirmPreset(null);
      setEdits({});
      setMsg({ kind: 'ok', text: t('preset_applied', { name: t(p.labelKey) }) });
      await mutate();
    } catch (e) {
      const info = errInfo(e);
      const text = !info
        ? t('save_error')
        : info.status === 403 ? `403 · ${t('err_403')}`
        : info.status === 401 ? `401 · ${t('err_401')}`
        : `${info.status}${info.detail ? ` · ${info.detail}` : ''}`;
      setMsg({ kind: 'err', text });
    } finally {
      setBusy(false);
    }
  }

  if (isLoading) {
    return <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)', padding: 16 }}>{t('loading')}</div>;
  }
  if (error) {
    return (
      <div className="mono" style={{ fontSize: 12, color: 'var(--down)', padding: 16 }}>
        {t('load_error')}
      </div>
    );
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {/* ── Derived gate state ────────────────────────────────────────────── */}
      <div
        style={{
          display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap',
          padding: '10px 12px', borderRadius: 'var(--r)',
          border: '1px solid var(--line)', background: 'var(--bg-inset)',
        }}
      >
        {blockers.length === 0 ? (
          <Badge variant="ok" dot>{t('state_can_enter')}</Badge>
        ) : (
          <Badge variant="micro" dot>{t('state_blocked')}</Badge>
        )}
        <span className="mono" style={{ fontSize: 11, color: 'var(--tx-dim)' }}>
          {blockers.length === 0
            ? t('state_can_enter_sub')
            : blockers.map((b) => t(b.reason)).join(' · ')}
        </span>
        {blockers.some((b) => b.key === 'bot.validation_gate.enabled') && (
          <button
            type="button"
            disabled={!canWrite || busy} suppressHydrationWarning
            onClick={quickUnblockValgate}
            style={{
              marginLeft: 'auto', padding: '5px 12px', borderRadius: 4,
              border: '1px solid var(--brand)', background: 'transparent',
              color: 'var(--brand)', fontSize: 11, fontWeight: 600,
              cursor: canWrite && !busy ? 'pointer' : 'not-allowed',
            }}
          >
            {t('unblock_now')}
          </button>
        )}
      </div>

      {/* ── Presets ───────────────────────────────────────────────────────── */}
      <div>
        <div className="mono" style={{ fontSize: 10, letterSpacing: '.06em', color: 'var(--tx-faint)', textTransform: 'uppercase', marginBottom: 8 }}>
          {t('presets_label')}
        </div>
        <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap' }}>
          {PRESETS.map((p) => (
            <button
              key={p.id}
              type="button"
              disabled={!canWrite || busy} suppressHydrationWarning
              onClick={() => setConfirmPreset(p)}
              style={{
                flex: '1 1 160px', minWidth: 150, padding: '12px 14px',
                borderRadius: 'var(--r)', cursor: canWrite && !busy ? 'pointer' : 'not-allowed',
                border: `1px solid ${toneColor[p.tone]}`,
                background: 'transparent', color: 'var(--tx-hi)',
                opacity: canWrite ? 1 : 0.5, textAlign: 'left',
              }}
            >
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <span className="bd-dot" style={{ background: toneColor[p.tone] }} />
                <strong style={{ fontSize: 13 }}>{t(p.labelKey)}</strong>
              </div>
              <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', marginTop: 6 }}>
                {t(`${p.labelKey}_sub`)}
              </div>
            </button>
          ))}
        </div>
      </div>

      {/* ── Preset confirmation ──────────────────────────────────────────── */}
      {confirmPreset && (
        <div
          style={{
            padding: 12, borderRadius: 'var(--r)',
            border: `1px solid ${toneColor[confirmPreset.tone]}`,
            background: 'rgba(233,162,62,.06)',
          }}
        >
          <div style={{ fontSize: 13, color: 'var(--tx-hi)', marginBottom: 8 }}>
            {t('confirm_preset', { name: t(confirmPreset.labelKey) })}
          </div>
          <div className="mono" style={{ fontSize: 11, color: 'var(--tx-dim)', marginBottom: 10, lineHeight: 1.6 }}>
            {confirmPreset.changes.map((c) => (
              <div key={c.key}>
                {c.key} → <span style={{ color: 'var(--tx-hi)' }}>{c.value}</span>
              </div>
            ))}
          </div>
          <div style={{ display: 'flex', gap: 8 }}>
            <button
              type="button" disabled={busy} suppressHydrationWarning
              onClick={() => doApplyPreset(confirmPreset)}
              style={{
                padding: '7px 14px', borderRadius: 4, border: 'none',
                background: toneColor[confirmPreset.tone], color: 'var(--bg)',
                fontWeight: 600, fontSize: 12, cursor: busy ? 'wait' : 'pointer',
              }}
            >
              {t('confirm_apply')}
            </button>
            <button
              type="button" disabled={busy} suppressHydrationWarning
              onClick={() => setConfirmPreset(null)}
              style={{
                padding: '7px 14px', borderRadius: 4,
                border: '1px solid var(--line)', background: 'transparent',
                color: 'var(--tx-dim)', fontSize: 12, cursor: 'pointer',
              }}
            >
              {t('cancel')}
            </button>
          </div>
        </div>
      )}

      {/* ── Advanced (per-knob) ──────────────────────────────────────────── */}
      <div>
        <button
          type="button"
          onClick={() => setShowAdvanced((s) => !s)}
          style={{
            display: 'flex', alignItems: 'center', gap: 6, background: 'none',
            border: 'none', cursor: 'pointer', color: 'var(--tx-dim)',
            fontSize: 12, padding: '4px 0',
          }}
        >
          <span>{showAdvanced ? '▾' : '▸'}</span> {t('advanced')}
        </button>

        {showAdvanced && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 4, marginTop: 8 }}>
            {KNOBS.map((k) => (
              <KnobRow
                key={k.key}
                knob={k}
                value={effVal(k.key)}
                meta={params[k.key]}
                dirty={k.key in edits}
                disabled={!canWrite}
                label={t(k.labelKey)}
                onChange={(v) => editKnob(k.key, v)}
              />
            ))}

            {/* Commit bar for advanced edits */}
            {dirtyKeys.length > 0 && (
              <div
                style={{
                  marginTop: 10, padding: 12, borderRadius: 'var(--r)',
                  border: '1px solid var(--brand)', background: 'rgba(70,200,216,.06)',
                }}
              >
                <div style={{ fontSize: 12, color: 'var(--tx-hi)', marginBottom: 8 }}>
                  {t('pending', { n: dirtyKeys.length })}
                </div>
                <input
                  value={reason}
                  onChange={(e) => setReason(e.target.value)}
                  placeholder={t('reason_placeholder')}
                  disabled={!canWrite || busy} suppressHydrationWarning
                  style={{
                    width: '100%', padding: '7px 10px', marginBottom: 8,
                    borderRadius: 4, border: '1px solid var(--line)',
                    background: 'var(--bg-void)', color: 'var(--tx-hi)', fontSize: 12,
                  }}
                />
                <div style={{ display: 'flex', gap: 8 }}>
                  <button
                    type="button"
                    disabled={!canWrite || !reasonOk || busy} suppressHydrationWarning
                    onClick={commitAdvanced}
                    style={{
                      padding: '7px 14px', borderRadius: 4, border: 'none',
                      background: reasonOk ? 'var(--brand)' : 'var(--line)',
                      color: 'var(--bg)', fontWeight: 600, fontSize: 12,
                      cursor: reasonOk && !busy ? 'pointer' : 'not-allowed',
                    }}
                  >
                    {t('apply_changes')}
                  </button>
                  <button
                    type="button" disabled={busy} suppressHydrationWarning
                    onClick={() => { setEdits({}); setReason(''); }}
                    style={{
                      padding: '7px 14px', borderRadius: 4,
                      border: '1px solid var(--line)', background: 'transparent',
                      color: 'var(--tx-dim)', fontSize: 12, cursor: 'pointer',
                    }}
                  >
                    {t('discard')}
                  </button>
                </div>
              </div>
            )}
          </div>
        )}
      </div>

      {/* ── Result banner ────────────────────────────────────────────────── */}
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

// ── Single knob row ──────────────────────────────────────────────────────────

interface KnobRowProps {
  knob: Knob;
  value: string;
  meta?: ParamItem;
  dirty: boolean;
  disabled: boolean;
  label: string;
  onChange: (v: string) => void;
}

function KnobRow({ knob, value, meta, dirty, disabled, label, onChange }: KnobRowProps) {
  const isDefault = meta ? value === meta.default : false;

  return (
    <div
      style={{
        display: 'grid', gridTemplateColumns: '200px 1fr 90px', gap: 12,
        alignItems: 'center', padding: '8px 10px', borderRadius: 6,
        background: dirty ? 'rgba(70,200,216,.08)' : 'transparent',
      }}
    >
      <div>
        <div style={{ fontSize: 12, color: 'var(--tx-hi)' }}>{label}</div>
        <div className="mono" style={{ fontSize: 9, color: 'var(--tx-faint)' }}>{knob.key}</div>
      </div>

      <div>
        {knob.type === 'bool' ? (
          <select
            value={value} disabled={disabled} suppressHydrationWarning
            onChange={(e) => onChange(e.target.value)}
            style={selectStyle}
          >
            <option value="true">ON</option>
            <option value="false">OFF</option>
          </select>
        ) : knob.type === 'enum' ? (
          <select
            value={value} disabled={disabled} suppressHydrationWarning
            onChange={(e) => onChange(e.target.value)}
            style={selectStyle}
          >
            {knob.enum!.map((o) => <option key={o} value={o}>{o}</option>)}
          </select>
        ) : knob.type === 'string' ? (
          <input
            value={value} disabled={disabled} suppressHydrationWarning
            onChange={(e) => onChange(e.target.value)}
            placeholder="—"
            style={{ ...selectStyle, width: '100%' }}
          />
        ) : (
          <input
            type="range"
            min={knob.min} max={knob.max} step={knob.step}
            value={parseFloat(value) || 0} disabled={disabled} suppressHydrationWarning
            onChange={(e) => onChange(e.target.value)}
            style={{ width: '100%', accentColor: 'var(--brand)' }}
          />
        )}
      </div>

      <div className="mono" style={{ fontSize: 12, textAlign: 'right', color: isDefault ? 'var(--tx-faint)' : 'var(--brand)' }}>
        {knob.type === 'bool' || knob.type === 'enum' || knob.type === 'string' ? '' : value}
      </div>
    </div>
  );
}

const selectStyle: React.CSSProperties = {
  padding: '5px 8px', borderRadius: 4, border: '1px solid var(--line)',
  background: 'var(--bg-void)', color: 'var(--tx-hi)', fontSize: 12,
};
