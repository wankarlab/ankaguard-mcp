'use client';

// ParameterBrowser — generic browser/editor over the full dynamic-config
// registry (35 groups, ~400 params). Two modes:
//   - "edit": each row writes live via PUT /params/:key (reason >= 6 chars).
//   - "pick": each row reports its chosen value up via onPick, for the mode
//             builder (ModeManager) — no live write happens here.
//
// Rendering is driven entirely by the registry type/min/max/enum/requires_restart
// returned by the backend; nothing is hardcoded per-key.

import { useEffect, useMemo, useState } from 'react';
import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { Badge } from '@/components/ui/Badge';
import { ApiError } from '@/lib/api';
import {
  getGroups,
  getParams,
  updateParam,
  type Group,
  type Param,
} from '@/lib/config-api';

function errInfo(e: unknown): { status: number; detail: string } | null {
  if (e instanceof ApiError) {
    const body = e.body as { error?: string; message?: string } | undefined;
    return { status: e.status, detail: body?.message || body?.error || '' };
  }
  return null;
}

export type BrowserMode = 'edit' | 'pick';

interface Props {
  mode?: BrowserMode;
  /** pick mode: report a chosen key/value to the parent. */
  onPick?: (key: string, value: string) => void;
  /** pick mode: keys already selected by the parent (for highlighting). */
  picked?: Record<string, string>;
  /** Pre-filter to a single group (locks the group selector). */
  filterGroup?: string;
  /** Backend-truth write gate. Edit mode is read-only when false. */
  canWrite?: boolean;
}

export function ParameterBrowser({
  mode = 'edit',
  onPick,
  picked,
  filterGroup,
  canWrite = true,
}: Props) {
  const t = useTranslations('Control');

  const { data: groups } = useSWR<Group[]>('config:groups', getGroups, {
    revalidateOnFocus: false,
  });

  const [activeGroup, setActiveGroup] = useState<string | null>(filterGroup ?? null);
  const [query, setQuery] = useState('');
  const [onlyModified, setOnlyModified] = useState(false);

  // Default to the first group once groups load (unless pinned via filterGroup).
  useEffect(() => {
    if (filterGroup) {
      setActiveGroup(filterGroup);
      return;
    }
    if (!activeGroup && groups && groups.length > 0) {
      setActiveGroup(groups[0].name);
    }
  }, [groups, activeGroup, filterGroup]);

  // Params for the active group, or all-modified when the checkbox is on.
  const swrKey = onlyModified
    ? 'config:params:modified'
    : activeGroup
      ? `config:params:${activeGroup}`
      : null;

  const { data: params, error, isLoading, mutate } = useSWR<Param[]>(
    swrKey,
    () =>
      onlyModified
        ? getParams({ modified: true })
        : getParams({ group: activeGroup! }),
    { revalidateOnFocus: false },
  );

  const filtered = useMemo(() => {
    const list = params ?? [];
    const q = query.trim().toLowerCase();
    if (!q) return list;
    return list.filter(
      (p) =>
        p.key.toLowerCase().includes(q) ||
        (p.description ?? '').toLowerCase().includes(q),
    );
  }, [params, query]);

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      {/* Header: search + filters */}
      <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap', alignItems: 'center' }}>
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t('search_placeholder')}
          style={{
            flex: '1 1 220px', minWidth: 180, padding: '7px 10px',
            borderRadius: 4, border: '1px solid var(--line)',
            background: 'var(--bg-void)', color: 'var(--tx-hi)', fontSize: 12,
          }}
        />
        <label
          className="mono"
          style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 11, color: 'var(--tx-dim)', cursor: 'pointer' }}
        >
          <input
            type="checkbox"
            checked={onlyModified}
            onChange={(e) => setOnlyModified(e.target.checked)}
            disabled={!!filterGroup}
          />
          {t('only_modified')}
        </label>
      </div>

      {/* Group tabs (hidden when pinned to a single group) */}
      {!filterGroup && !onlyModified && (
        <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
          {(groups ?? []).map((g, i) => (
            <button
              key={g.name ?? i}
              type="button"
              onClick={() => setActiveGroup(g.name)}
              className="mono"
              style={{
                padding: '5px 10px', borderRadius: 4, fontSize: 11,
                cursor: 'pointer',
                border: `1px solid ${activeGroup === g.name ? 'var(--brand)' : 'var(--line)'}`,
                background: activeGroup === g.name ? 'rgba(70,200,216,.08)' : 'transparent',
                color: activeGroup === g.name ? 'var(--brand)' : 'var(--tx-dim)',
              }}
            >
              {g.name}
              {typeof g.param_count === 'number' && (
                <span style={{ color: 'var(--tx-faint)', marginLeft: 6 }}>{g.param_count}</span>
              )}
            </button>
          ))}
        </div>
      )}

      {/* Param rows */}
      {isLoading ? (
        <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)', padding: 12 }}>
          {t('loading')}
        </div>
      ) : error ? (
        <div className="mono" style={{ fontSize: 12, color: 'var(--down)', padding: 12 }}>
          {t('load_error')}
        </div>
      ) : filtered.length === 0 ? (
        <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)', padding: 12 }}>
          {t('no_params')}
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
          {filtered.map((p, i) => (
            <ParamRow
              key={p.key ?? i}
              param={p}
              mode={mode}
              canWrite={canWrite}
              pickedValue={picked?.[p.key]}
              onPick={onPick}
              onSaved={() => mutate()}
            />
          ))}
        </div>
      )}
    </div>
  );
}

// ── Single param row ──────────────────────────────────────────────────────────

interface RowProps {
  param: Param;
  mode: BrowserMode;
  canWrite: boolean;
  pickedValue?: string;
  onPick?: (key: string, value: string) => void;
  onSaved: () => void;
}

function ParamRow({ param, mode, canWrite, pickedValue, onPick, onSaved }: RowProps) {
  const t = useTranslations('Control');

  const effective = param.current_effective_value ?? param.default ?? '';
  // Local draft: in pick mode, default to the already-picked value if present.
  const [draft, setDraft] = useState<string>(pickedValue ?? effective);
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  // Keep the draft in sync if the parent's picked value changes externally.
  useEffect(() => {
    if (pickedValue !== undefined) setDraft(pickedValue);
  }, [pickedValue]);

  const isPicked = pickedValue !== undefined;
  const dirty = draft !== effective;
  const reasonOk = reason.trim().length >= 6;
  const isDefault = draft === param.default;

  function onInput(v: string) {
    setMsg(null);
    setDraft(v);
    // In pick mode, report selections immediately (optimistic, no server call).
    if (mode === 'pick' && onPick) onPick(param.key, v);
  }

  async function commit() {
    if (mode !== 'edit' || !canWrite || !reasonOk || !dirty || busy) return;
    setBusy(true);
    setMsg(null);
    try {
      await updateParam(param.key, draft, reason.trim());
      setReason('');
      setMsg({ kind: 'ok', text: t('saved') });
      onSaved();
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

  return (
    <div
      style={{
        padding: '10px 12px', borderRadius: 6,
        border: '1px solid var(--line)',
        background: isPicked ? 'rgba(70,200,216,.06)' : dirty ? 'rgba(233,162,62,.05)' : 'transparent',
      }}
    >
      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 10 }}>
        <div style={{ minWidth: 0, flex: 1 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
            <span className="mono" style={{ fontSize: 11, color: 'var(--tx-hi)' }}>{param.key}</span>
            {param.requires_restart && (
              <Badge variant="micro">{t('requires_restart')}</Badge>
            )}
            {param.has_override && !param.requires_restart && (
              <Badge variant="brand">{t('overridden')}</Badge>
            )}
          </div>
          {param.description && (
            <div style={{ fontSize: 11, color: 'var(--tx-dim)', marginTop: 3 }}>{param.description}</div>
          )}
          <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', marginTop: 3 }}>
            {t('default_label')}: {param.default || '—'}
            {' · '}
            {t('current_label')}: {effective || '—'}
          </div>
        </div>

        {mode === 'pick' && (
          <label
            className="mono"
            style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 10, color: 'var(--tx-dim)', whiteSpace: 'nowrap', cursor: 'pointer' }}
          >
            <input
              type="checkbox"
              checked={isPicked}
              onChange={(e) => {
                if (e.target.checked) onPick?.(param.key, draft);
                else onPick?.(param.key, ''); // parent removes key on empty sentinel
              }}
            />
            {t('include')}
          </label>
        )}
      </div>

      {/* Input control by type */}
      <div style={{ marginTop: 8 }}>
        <ParamInput param={param} value={draft} disabled={mode === 'edit' && !canWrite} onChange={onInput} />
      </div>

      {/* Edit-mode commit affordance */}
      {mode === 'edit' && dirty && (
        <div style={{ marginTop: 8, display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center' }}>
          <input
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            placeholder={t('reason_placeholder')}
            disabled={!canWrite || busy}
            style={{
              flex: '1 1 200px', minWidth: 160, padding: '6px 9px', borderRadius: 4,
              border: '1px solid var(--line)', background: 'var(--bg-void)',
              color: 'var(--tx-hi)', fontSize: 11,
            }}
          />
          <button
            type="button"
            disabled={!canWrite || !reasonOk || busy}
            onClick={commit}
            style={{
              padding: '6px 12px', borderRadius: 4, border: 'none',
              background: reasonOk ? 'var(--brand)' : 'var(--line)', color: 'var(--bg)',
              fontWeight: 600, fontSize: 11, cursor: reasonOk && !busy ? 'pointer' : 'not-allowed',
            }}
          >
            {t('apply')}
          </button>
          <button
            type="button"
            disabled={busy}
            onClick={() => { setDraft(effective); setReason(''); setMsg(null); }}
            style={{
              padding: '6px 12px', borderRadius: 4, border: '1px solid var(--line)',
              background: 'transparent', color: 'var(--tx-dim)', fontSize: 11, cursor: 'pointer',
            }}
          >
            {t('discard')}
          </button>
        </div>
      )}

      {!isDefault && mode === 'edit' && !dirty && (
        <div className="mono" style={{ fontSize: 9, color: 'var(--tx-faint)', marginTop: 4 }}>
          {t('not_default')}
        </div>
      )}

      {msg && (
        <div
          className="mono"
          style={{
            marginTop: 6, fontSize: 10, padding: '5px 8px', borderRadius: 4,
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

// ── Type-driven input ─────────────────────────────────────────────────────────

interface InputProps {
  param: Param;
  value: string;
  disabled: boolean;
  onChange: (v: string) => void;
}

const inputStyle: React.CSSProperties = {
  padding: '6px 9px', borderRadius: 4, border: '1px solid var(--line)',
  background: 'var(--bg-void)', color: 'var(--tx-hi)', fontSize: 12,
};

function ParamInput({ param, value, disabled, onChange }: InputProps) {
  const type = param.type;

  // bool -> toggle (rendered as a two-state select for consistency with scalping)
  if (type === 'bool') {
    return (
      <select value={value} disabled={disabled} onChange={(e) => onChange(e.target.value)} style={inputStyle}>
        <option value="true">ON</option>
        <option value="false">OFF</option>
      </select>
    );
  }

  // enum / string with enum_values -> select
  if (type === 'enum' || (param.enum_values && param.enum_values.length > 0)) {
    return (
      <select value={value} disabled={disabled} onChange={(e) => onChange(e.target.value)} style={inputStyle}>
        {(param.enum_values ?? []).map((o, i) => <option key={o ?? i} value={o}>{o}</option>)}
      </select>
    );
  }

  // json -> textarea
  if (type === 'json') {
    return (
      <textarea
        value={value}
        disabled={disabled}
        onChange={(e) => onChange(e.target.value)}
        rows={4}
        spellCheck={false}
        style={{ ...inputStyle, width: '100%', fontFamily: 'var(--mono)', resize: 'vertical' }}
      />
    );
  }

  // duration -> text with suffix hint
  if (type === 'duration') {
    return (
      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
        <input
          value={value}
          disabled={disabled}
          onChange={(e) => onChange(e.target.value)}
          placeholder="e.g. 30s / 5m / 1h"
          style={{ ...inputStyle, flex: '1 1 140px' }}
        />
        <span className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)' }}>s · m · h</span>
      </div>
    );
  }

  // float / int -> slider + numeric input when bounded; plain numeric otherwise
  if (type === 'float' || type === 'int') {
    const hasBounds = typeof param.min === 'number' && typeof param.max === 'number';
    const step = param.step ?? (type === 'int' ? 1 : 0.01);
    return (
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        {hasBounds && (
          <input
            type="range"
            min={param.min}
            max={param.max}
            step={step}
            value={parseFloat(value) || 0}
            disabled={disabled}
            onChange={(e) => onChange(e.target.value)}
            style={{ flex: 1, accentColor: 'var(--brand)' }}
          />
        )}
        <input
          type="number"
          min={param.min}
          max={param.max}
          step={step}
          value={value}
          disabled={disabled}
          onChange={(e) => onChange(e.target.value)}
          style={{ ...inputStyle, width: 110 }}
        />
        {param.unit && <span className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)' }}>{param.unit}</span>}
      </div>
    );
  }

  // free string -> text input
  return (
    <input
      value={value}
      disabled={disabled}
      onChange={(e) => onChange(e.target.value)}
      placeholder="—"
      style={{ ...inputStyle, width: '100%' }}
    />
  );
}
