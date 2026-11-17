'use client';

// ModeManager — CRUD + apply/rollback over user-defined "modes" (presets with
// category="mode"). A mode is a coherent bundle of config keys+values the
// operator can apply atomically to the live bot. The param picker reuses
// ParameterBrowser in "pick" mode so the user composes a mode from the same
// registry-driven UI used elsewhere.

import { useMemo, useState } from 'react';
import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { Badge } from '@/components/ui/Badge';
import { ApiError } from '@/lib/api';
import { ParameterBrowser } from './ParameterBrowser';
import {
  getPresets,
  getActiveMode,
  getParams,
  createPreset,
  updatePreset,
  deletePreset,
  applyPreset,
  rollbackPreset,
  type Preset,
  type CreatePresetInput,
  type Param,
} from '@/lib/config-api';

function errInfo(e: unknown): { status: number; detail: string } | null {
  if (e instanceof ApiError) {
    const body = e.body as { error?: string; message?: string } | undefined;
    return { status: e.status, detail: body?.message || body?.error || '' };
  }
  return null;
}

function errText(e: unknown, t: ReturnType<typeof useTranslations>): string {
  const info = errInfo(e);
  if (!info) return t('save_error');
  if (info.status === 403) return `403 · ${t('err_403')}`;
  if (info.status === 401) return `401 · ${t('err_401')}`;
  return `${info.status}${info.detail ? ` · ${info.detail}` : ''}`;
}

// Example seed modes. These are USER DATA written via the API (not hardcoded
// backend constants) — the operator can edit or delete them afterwards.
const EXAMPLE_MODES: CreatePresetInput[] = [
  {
    name: 'agresivo',
    risk_label: 'agresivo',
    description: 'Abre el stack para scalping 5/15/30m.',
    params: {
      'scalping.confidence_threshold': '0.60',
      'scalping.trailing_stop_pct': '1.3',
      'bot.max_concurrent_trades': '5',
    },
  },
  {
    name: 'moderado',
    risk_label: 'moderado',
    description: 'Equilibrio entre selectividad y actividad.',
    params: {
      'scalping.confidence_threshold': '0.65',
      'scalping.trailing_stop_pct': '1.5',
      'bot.max_concurrent_trades': '3',
    },
  },
  {
    name: 'seguro',
    risk_label: 'seguro',
    description: 'Selectivo, edge estricto, baja exposición.',
    params: {
      'scalping.confidence_threshold': '0.72',
      'scalping.trailing_stop_pct': '2.0',
      'bot.max_concurrent_trades': '1',
    },
  },
];

interface Props {
  canWrite: boolean;
}

type View =
  | { kind: 'list' }
  | { kind: 'create' }
  | { kind: 'edit'; mode: Preset };

export function ModeManager({ canWrite }: Props) {
  const t = useTranslations('Control');

  const { data: modes, error, isLoading, mutate } = useSWR<Preset[]>(
    'config:modes',
    () => getPresets('mode'),
    { revalidateOnFocus: false, refreshInterval: 20_000 },
  );
  const { data: active, mutate: mutateActive } = useSWR<Preset | null>(
    'config:active-mode',
    getActiveMode,
    { revalidateOnFocus: false, refreshInterval: 20_000 },
  );

  const [view, setView] = useState<View>({ kind: 'list' });
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  // Two-step confirmations keyed by mode name.
  const [confirmApply, setConfirmApply] = useState<Preset | null>(null);
  const [applyReason, setApplyReason] = useState('');
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [confirmRollback, setConfirmRollback] = useState<string | null>(null);
  // Diff view (mode params vs current effective values).
  const [diffMode, setDiffMode] = useState<Preset | null>(null);

  async function refresh() {
    await Promise.all([mutate(), mutateActive()]);
  }

  async function seedExamples() {
    if (!canWrite || busy) return;
    setBusy(true);
    setMsg(null);
    try {
      for (const m of EXAMPLE_MODES) {
        await createPreset(m);
      }
      setMsg({ kind: 'ok', text: t('examples_created') });
      await refresh();
    } catch (e) {
      setMsg({ kind: 'err', text: errText(e, t) });
    } finally {
      setBusy(false);
    }
  }

  async function doApply(mode: Preset) {
    if (!canWrite || busy) return;
    const reason = applyReason.trim() || `apply mode:${mode.name} via control panel`;
    setBusy(true);
    setMsg(null);
    try {
      await applyPreset(mode.name, reason);
      setConfirmApply(null);
      setApplyReason('');
      setMsg({ kind: 'ok', text: t('mode_applied', { name: mode.name }) });
      await refresh();
    } catch (e) {
      setMsg({ kind: 'err', text: errText(e, t) });
    } finally {
      setBusy(false);
    }
  }

  async function doRollback(name: string) {
    if (!canWrite || busy) return;
    setBusy(true);
    setMsg(null);
    try {
      await rollbackPreset(name);
      setConfirmRollback(null);
      setMsg({ kind: 'ok', text: t('mode_rolled_back', { name }) });
      await refresh();
    } catch (e) {
      setMsg({ kind: 'err', text: errText(e, t) });
    } finally {
      setBusy(false);
    }
  }

  async function doDelete(name: string) {
    if (!canWrite || busy) return;
    setBusy(true);
    setMsg(null);
    try {
      await deletePreset(name);
      setConfirmDelete(null);
      setMsg({ kind: 'ok', text: t('mode_deleted', { name }) });
      await refresh();
    } catch (e) {
      setMsg({ kind: 'err', text: errText(e, t) });
    } finally {
      setBusy(false);
    }
  }

  // ── Editor (create / edit) ──────────────────────────────────────────────────
  if (view.kind === 'create' || view.kind === 'edit') {
    return (
      <ModeEditor
        canWrite={canWrite}
        initial={view.kind === 'edit' ? view.mode : null}
        onCancel={() => setView({ kind: 'list' })}
        onSaved={async () => {
          setView({ kind: 'list' });
          await refresh();
          setMsg({ kind: 'ok', text: t('mode_saved') });
        }}
      />
    );
  }

  // ── List ────────────────────────────────────────────────────────────────────
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      {/* Active mode banner + actions */}
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 10, flexWrap: 'wrap' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          {active ? (
            <Badge variant="ok" dot>{t('active_mode', { name: active.name })}</Badge>
          ) : (
            <Badge variant="neutral">{t('no_active_mode')}</Badge>
          )}
        </div>
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
          <button
            type="button"
            disabled={!canWrite || busy}
            onClick={() => setView({ kind: 'create' })}
            style={primaryBtn(canWrite && !busy)}
          >
            {t('create_mode')}
          </button>
          {(modes?.length ?? 0) === 0 && (
            <button
              type="button"
              disabled={!canWrite || busy}
              onClick={seedExamples}
              style={ghostBtn(canWrite && !busy)}
            >
              {t('seed_examples')}
            </button>
          )}
        </div>
      </div>

      {/* List */}
      {isLoading ? (
        <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)', padding: 12 }}>{t('loading')}</div>
      ) : error ? (
        <div className="mono" style={{ fontSize: 12, color: 'var(--down)', padding: 12 }}>{t('load_error')}</div>
      ) : (modes?.length ?? 0) === 0 ? (
        <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)', padding: 12 }}>{t('no_modes')}</div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {modes!.map((m) => (
            <div
              key={m.name}
              style={{
                padding: '12px 14px', borderRadius: 'var(--r)',
                border: `1px solid ${m.is_active ? 'var(--up)' : 'var(--line)'}`,
                background: m.is_active ? 'rgba(74,219,132,.05)' : 'var(--bg-inset)',
              }}
            >
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 10, flexWrap: 'wrap' }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                  <strong style={{ fontSize: 14, color: 'var(--tx-hi)' }}>{m.name}</strong>
                  {m.risk_label && <Badge variant="brand">{m.risk_label}</Badge>}
                  {m.is_active && <Badge variant="ok" dot>{t('badge_active')}</Badge>}
                  <span className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)' }}>
                    {t('param_count', { n: Object.keys(m.params).length })}
                  </span>
                  {m.last_applied_at && (
                    <span className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)' }}>
                      {t('last_applied')}: {new Date(m.last_applied_at).toLocaleString()}
                    </span>
                  )}
                </div>
              </div>
              {m.description && (
                <div style={{ fontSize: 11, color: 'var(--tx-dim)', marginTop: 4 }}>{m.description}</div>
              )}

              <div style={{ display: 'flex', gap: 8, marginTop: 10, flexWrap: 'wrap' }}>
                <button type="button" disabled={!canWrite || busy} onClick={() => { setConfirmApply(m); setApplyReason(''); }} style={primaryBtn(canWrite && !busy)}>
                  {t('apply')}
                </button>
                <button type="button" disabled={!canWrite || busy} onClick={() => setConfirmRollback(m.name)} style={ghostBtn(canWrite && !busy)}>
                  {t('rollback')}
                </button>
                <button type="button" onClick={() => setDiffMode(diffMode?.name === m.name ? null : m)} style={ghostBtn(true)}>
                  {diffMode?.name === m.name ? t('hide_diff') : t('show_diff')}
                </button>
                {m.editable !== false && (
                  <>
                    <button type="button" disabled={!canWrite || busy} onClick={() => setView({ kind: 'edit', mode: m })} style={ghostBtn(canWrite && !busy)}>
                      {t('edit')}
                    </button>
                    <button type="button" disabled={!canWrite || busy} onClick={() => setConfirmDelete(m.name)} style={dangerBtn(canWrite && !busy)}>
                      {t('delete')}
                    </button>
                  </>
                )}
              </div>

              {/* Inline confirmations */}
              {confirmApply?.name === m.name && (
                <div style={confirmBox('var(--brand)')}>
                  <div style={{ fontSize: 12, color: 'var(--tx-hi)', marginBottom: 8 }}>
                    {t('confirm_apply', { name: m.name, n: Object.keys(m.params).length })}
                  </div>
                  <input
                    value={applyReason}
                    onChange={(e) => setApplyReason(e.target.value)}
                    placeholder={t('reason_placeholder')}
                    disabled={busy}
                    style={{ width: '100%', padding: '6px 9px', marginBottom: 8, borderRadius: 4, border: '1px solid var(--line)', background: 'var(--bg-void)', color: 'var(--tx-hi)', fontSize: 11 }}
                  />
                  <div style={{ display: 'flex', gap: 8 }}>
                    <button type="button" disabled={busy} onClick={() => doApply(m)} style={primaryBtn(!busy)}>{t('confirm')}</button>
                    <button type="button" disabled={busy} onClick={() => setConfirmApply(null)} style={ghostBtn(!busy)}>{t('cancel')}</button>
                  </div>
                </div>
              )}
              {confirmRollback === m.name && (
                <ConfirmInline
                  label={t('confirm_rollback', { name: m.name })}
                  busy={busy}
                  onConfirm={() => doRollback(m.name)}
                  onCancel={() => setConfirmRollback(null)}
                  tone="var(--brand)"
                  confirmText={t('confirm')}
                  cancelText={t('cancel')}
                />
              )}
              {confirmDelete === m.name && (
                <ConfirmInline
                  label={t('confirm_delete', { name: m.name })}
                  busy={busy}
                  onConfirm={() => doDelete(m.name)}
                  onCancel={() => setConfirmDelete(null)}
                  tone="var(--down)"
                  confirmText={t('confirm_delete_btn')}
                  cancelText={t('cancel')}
                />
              )}

              {/* Diff: mode params vs effective */}
              {diffMode?.name === m.name && <ModeDiff mode={m} />}
            </div>
          ))}
        </div>
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

// ── Diff: mode params vs current effective values ─────────────────────────────

function ModeDiff({ mode }: { mode: Preset }) {
  const t = useTranslations('Control');
  const { data: allParams } = useSWR<Param[]>('config:params:all', () => getParams(), {
    revalidateOnFocus: false,
  });

  const rows = useMemo(() => {
    const byKey = new Map((allParams ?? []).map((p) => [p.key, p]));
    return Object.entries(mode.params).map(([key, modeVal]) => {
      const eff = byKey.get(key)?.current_effective_value ?? '';
      return { key, modeVal, eff, changed: modeVal !== eff };
    });
  }, [allParams, mode.params]);

  return (
    <div style={{ marginTop: 10, padding: 10, borderRadius: 6, border: '1px solid var(--line)', background: 'var(--bg-void)' }}>
      <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', textTransform: 'uppercase', letterSpacing: '.06em', marginBottom: 6 }}>
        {t('diff_title')}
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
        {rows.map((r) => (
          <div key={r.key} className="mono" style={{ fontSize: 10, display: 'grid', gridTemplateColumns: '1fr auto auto', gap: 10, alignItems: 'center' }}>
            <span style={{ color: 'var(--tx-dim)' }}>{r.key}</span>
            <span style={{ color: 'var(--tx-faint)' }}>{t('diff_current')}: {r.eff || '—'}</span>
            <span style={{ color: r.changed ? 'var(--brand)' : 'var(--tx-faint)' }}>{t('diff_mode')}: {r.modeVal}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

// ── Mode editor (create / edit) ───────────────────────────────────────────────

interface EditorProps {
  canWrite: boolean;
  initial: Preset | null;
  onCancel: () => void;
  onSaved: () => void;
}

function ModeEditor({ canWrite, initial, onCancel, onSaved }: EditorProps) {
  const t = useTranslations('Control');
  const isEdit = initial != null;

  const [name, setName] = useState(initial?.name ?? '');
  const [riskLabel, setRiskLabel] = useState(initial?.risk_label ?? '');
  const [description, setDescription] = useState(initial?.description ?? '');
  // Selected params: key -> value. Empty-string value from the picker means "remove".
  const [picked, setPicked] = useState<Record<string, string>>(initial?.params ?? {});
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  const pickedCount = Object.keys(picked).length;
  const canSave = canWrite && name.trim().length > 0 && pickedCount > 0 && !busy;

  function onPick(key: string, value: string) {
    setPicked((prev) => {
      const next = { ...prev };
      if (value === '') delete next[key];
      else next[key] = value;
      return next;
    });
  }

  async function save() {
    if (!canSave) return;
    setBusy(true);
    setMsg(null);
    const payload: CreatePresetInput = {
      name: name.trim(),
      risk_label: riskLabel.trim(),
      description: description.trim(),
      params: picked,
    };
    try {
      if (isEdit) {
        await updatePreset(initial!.name, payload);
      } else {
        await createPreset(payload);
      }
      onSaved();
    } catch (e) {
      setMsg({ kind: 'err', text: errText(e, t) });
    } finally {
      setBusy(false);
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <strong style={{ fontSize: 14, color: 'var(--tx-hi)' }}>
          {isEdit ? t('edit_mode_title', { name: initial!.name }) : t('create_mode_title')}
        </strong>
        <button type="button" onClick={onCancel} style={ghostBtn(true)}>{t('back')}</button>
      </div>

      <div style={{ display: 'grid', gap: 10, gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))' }}>
        <label style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
          <span className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)' }}>{t('field_name')}</span>
          <input value={name} disabled={isEdit} onChange={(e) => setName(e.target.value)} style={fieldStyle} />
        </label>
        <label style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
          <span className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)' }}>{t('field_risk')}</span>
          <input value={riskLabel} onChange={(e) => setRiskLabel(e.target.value)} style={fieldStyle} />
        </label>
        <label style={{ display: 'flex', flexDirection: 'column', gap: 4, gridColumn: '1 / -1' }}>
          <span className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)' }}>{t('field_description')}</span>
          <input value={description} onChange={(e) => setDescription(e.target.value)} style={fieldStyle} />
        </label>
      </div>

      <div>
        <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', textTransform: 'uppercase', letterSpacing: '.06em', marginBottom: 8 }}>
          {t('mode_params_label')} · {t('param_count', { n: pickedCount })}
        </div>
        <ParameterBrowser mode="pick" onPick={onPick} picked={picked} canWrite={canWrite} />
      </div>

      <div style={{ display: 'flex', gap: 8 }}>
        <button type="button" disabled={!canSave} onClick={save} style={primaryBtn(canSave)}>
          {isEdit ? t('save_changes') : t('create')}
        </button>
        <button type="button" disabled={busy} onClick={onCancel} style={ghostBtn(!busy)}>{t('cancel')}</button>
      </div>

      {msg && (
        <div className="mono" style={{ fontSize: 11, padding: '8px 12px', borderRadius: 'var(--r)', color: 'var(--down)', background: 'rgba(233,90,90,.07)' }}>
          {msg.text}
        </div>
      )}
    </div>
  );
}

// ── Small shared UI helpers ───────────────────────────────────────────────────

function ConfirmInline({
  label, busy, onConfirm, onCancel, tone, confirmText, cancelText,
}: {
  label: string; busy: boolean; onConfirm: () => void; onCancel: () => void;
  tone: string; confirmText: string; cancelText: string;
}) {
  return (
    <div style={confirmBox(tone)}>
      <div style={{ fontSize: 12, color: 'var(--tx-hi)', marginBottom: 8 }}>{label}</div>
      <div style={{ display: 'flex', gap: 8 }}>
        <button
          type="button" disabled={busy} onClick={onConfirm}
          style={{ padding: '6px 12px', borderRadius: 4, border: 'none', background: tone, color: 'var(--bg)', fontWeight: 600, fontSize: 11, cursor: busy ? 'wait' : 'pointer' }}
        >
          {confirmText}
        </button>
        <button type="button" disabled={busy} onClick={onCancel} style={ghostBtn(!busy)}>{cancelText}</button>
      </div>
    </div>
  );
}

const fieldStyle: React.CSSProperties = {
  padding: '7px 10px', borderRadius: 4, border: '1px solid var(--line)',
  background: 'var(--bg-void)', color: 'var(--tx-hi)', fontSize: 12,
};

function primaryBtn(enabled: boolean): React.CSSProperties {
  return {
    padding: '7px 14px', borderRadius: 4, border: 'none',
    background: enabled ? 'var(--brand)' : 'var(--line)', color: 'var(--bg)',
    fontWeight: 600, fontSize: 12, cursor: enabled ? 'pointer' : 'not-allowed',
  };
}
function ghostBtn(enabled: boolean): React.CSSProperties {
  return {
    padding: '7px 14px', borderRadius: 4, border: '1px solid var(--line)',
    background: 'transparent', color: 'var(--tx-dim)', fontSize: 12,
    cursor: enabled ? 'pointer' : 'not-allowed', opacity: enabled ? 1 : 0.5,
  };
}
function dangerBtn(enabled: boolean): React.CSSProperties {
  return {
    padding: '7px 14px', borderRadius: 4, border: '1px solid var(--down)',
    background: 'transparent', color: 'var(--down)', fontSize: 12,
    cursor: enabled ? 'pointer' : 'not-allowed', opacity: enabled ? 1 : 0.5,
  };
}
function confirmBox(tone: string): React.CSSProperties {
  return {
    marginTop: 10, padding: 12, borderRadius: 'var(--r)',
    border: `1px solid ${tone}`, background: 'rgba(233,162,62,.06)',
  };
}
