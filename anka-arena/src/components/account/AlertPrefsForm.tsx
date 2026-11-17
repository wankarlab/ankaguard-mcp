'use client';

// AlertPrefsForm — edits the 9 xsec.alert.* params through the existing
// /xsec/params endpoints (D8). Client-side range validation; backend errors are
// shown verbatim; after saving, the form is re-seeded from the values the
// backend returns (the confirmed ones), never from the local draft.

import { useEffect, useMemo, useState } from 'react';
import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { ApiError } from '@/lib/api';
import { getXsecParams, putXsecParam, type XSecApiErrorBody, type XSecParam } from '@/lib/xsec-api';
import {
  ALERT_KEY_LIST,
  BOOL_FIELDS,
  NUMERIC_FIELDS,
  diffAlertForms,
  parseAlertParams,
  validateAlertForm,
  type AlertForm,
  type BoolField,
  type NumericField,
} from '@/lib/alert-prefs';

function backendError(e: unknown, fallback: string): string {
  if (e instanceof ApiError) {
    const detail = (e.body as XSecApiErrorBody | undefined)?.error?.message;
    return `${e.status}${detail ? ` · ${detail}` : ''}`;
  }
  return fallback;
}

const inputStyle = {
  padding: '6px 8px', borderRadius: 4, border: '1px solid var(--line-2)',
  background: 'var(--bg-void)', color: 'var(--tx-hi)', fontSize: 12, width: 110,
} as const;

export function AlertPrefsForm({ canWrite }: { canWrite: boolean }) {
  const t = useTranslations('AccountAlerts');

  const { data, error, isLoading, mutate } = useSWR<XSecParam[]>('xsec:params', getXsecParams, {
    revalidateOnFocus: false,
  });

  const parsed = useMemo(() => (data ? parseAlertParams(data) : null), [data]);
  const [draft, setDraft] = useState<AlertForm | null>(null);
  const [saving, setSaving] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  // Seed (and re-seed after each save) the draft from backend values.
  useEffect(() => {
    if (parsed) setDraft(parsed.form);
  }, [parsed]);

  const errors = useMemo(() => (draft ? validateAlertForm(draft) : {}), [draft]);
  const changes = useMemo(() => (parsed && draft ? diffAlertForms(parsed.form, draft) : []), [parsed, draft]);
  const invalidFields = Object.keys(errors) as NumericField[];

  if (isLoading && !data) {
    return <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)', padding: 16 }}>{t('loading')}</div>;
  }
  if (error && !data) {
    return <div className="mono" style={{ fontSize: 12, color: 'var(--down)', padding: 16 }}>{t('load_error')}</div>;
  }
  if (!parsed || !draft) return null;

  // First reason why saving is disabled (shown next to the button).
  let disabledReason: string | null = null;
  if (!canWrite) disabledReason = t('reason_read_only');
  else if (invalidFields.length > 0) disabledReason = t('reason_invalid', { field: t(`f_${invalidFields[0]}`) });
  else if (changes.length === 0) disabledReason = t('reason_no_changes');
  else if (saving) disabledReason = t('saving');

  function setNum(f: NumericField, v: string) {
    setDraft((d) => (d ? { ...d, [f]: v } : d));
    setMsg(null);
  }
  function setBool(f: BoolField, v: boolean) {
    setDraft((d) => (d ? { ...d, [f]: v } : d));
    setMsg(null);
  }

  async function save() {
    if (!draft || disabledReason) return;
    setSaving(true);
    setMsg(null);
    const pending = diffAlertForms(parsed!.form, draft);
    let done = 0;
    try {
      for (const [key, value] of pending) {
        await putXsecParam(key, value);
        done++;
      }
      const fresh = await mutate(); // confirmed values come back from the backend
      const saved = fresh ? parseAlertParams(fresh).form : null;
      setMsg({ kind: 'ok', text: t('saved', { count: done }) });
      if (saved) setDraft(saved);
    } catch (e) {
      await mutate(); // reflect whatever was persisted before the failure
      setMsg({ kind: 'err', text: t('save_error', { done, total: pending.length, detail: backendError(e, t('network_error')) }) });
    } finally {
      setSaving(false);
    }
  }

  const rowStyle = { display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap', padding: '8px 0', borderTop: '1px solid var(--line)' } as const;

  return (
    <div style={{ display: 'flex', flexDirection: 'column' }}>
      {parsed.missing.length > 0 && (
        <div className="mono" style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginBottom: 8 }}>
          {t('defaults_note', { count: parsed.missing.length })}
        </div>
      )}
      <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', marginBottom: 4 }}>
        {ALERT_KEY_LIST.length} {t('keys_label')}
      </div>

      {BOOL_FIELDS.map((f) => (
        <label key={f} style={{ ...rowStyle, cursor: canWrite ? 'pointer' : 'not-allowed' }}>
          <input
            type="checkbox"
            checked={draft[f]}
            disabled={!canWrite || saving}
            onChange={(e) => setBool(f, e.target.checked)}
            suppressHydrationWarning
          />
          <span style={{ flex: 1 }}>
            <span style={{ color: 'var(--tx-hi)', fontSize: 12.5 }}>{t(`f_${f}`)}</span>
            <span style={{ display: 'block', color: 'var(--tx-faint)', fontSize: 11 }}>{t(`h_${f}`)}</span>
          </span>
        </label>
      ))}

      {NUMERIC_FIELDS.map((f) => {
        const err = errors[f];
        return (
          <div key={f} style={rowStyle}>
            <label htmlFor={`alert-${f}`} style={{ flex: 1, minWidth: 200 }}>
              <span style={{ color: 'var(--tx-hi)', fontSize: 12.5 }}>{t(`f_${f}`)}</span>
              <span style={{ display: 'block', color: 'var(--tx-faint)', fontSize: 11 }}>{t(`h_${f}`)}</span>
            </label>
            <div>
              <input
                id={`alert-${f}`}
                inputMode="decimal"
                value={draft[f]}
                disabled={!canWrite || saving}
                onChange={(e) => setNum(f, e.target.value)}
                aria-invalid={!!err}
                suppressHydrationWarning
                style={{ ...inputStyle, borderColor: err ? 'var(--down)' : 'var(--line-2)' }}
              />
              {err && <div className="mono" style={{ fontSize: 10.5, color: 'var(--down)', marginTop: 3 }}>{t(`e_${err}`)}</div>}
            </div>
          </div>
        );
      })}

      <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap', paddingTop: 12, borderTop: '1px solid var(--line)' }}>
        <button
          type="button"
          disabled={!!disabledReason}
          onClick={() => void save()}
          suppressHydrationWarning
          style={{
            padding: '6px 16px', borderRadius: 4, border: 'none', fontSize: 12, fontWeight: 600,
            background: disabledReason ? 'var(--line)' : 'var(--brand)',
            color: disabledReason ? 'var(--tx-faint)' : 'var(--bg)',
            cursor: disabledReason ? 'not-allowed' : 'pointer',
          }}
        >
          {saving ? t('saving') : t('save')}
        </button>
        {disabledReason && !saving && (
          <span className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{disabledReason}</span>
        )}
        {msg && (
          <span className="mono" style={{ fontSize: 11.5, color: msg.kind === 'ok' ? 'var(--up)' : 'var(--down)' }}>{msg.text}</span>
        )}
      </div>
    </div>
  );
}
