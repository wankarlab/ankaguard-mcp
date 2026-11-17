'use client';

// ParamsEditor — table of xsec_params (JWT read) with inline edit → PUT
// (JWT, Level 2, enforced server-side). Only renders keys the backend actually
// returns — no hardcoded param list.

import { useState } from 'react';
import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { ApiError } from '@/lib/api';
import { getXsecParams, putXsecParam, type XSecParam, type XSecApiErrorBody } from '@/lib/xsec-api';

// Backend error bodies are models.APIResponse: `error` is an object
// ({code,message}), not a bare string — see XSecApiErrorBody in xsec-api.ts.
function errText(e: unknown, t: ReturnType<typeof useTranslations>): string {
  if (e instanceof ApiError) {
    if (e.status === 403) return `403 · ${t('err_403')}`;
    if (e.status === 401) return `401 · ${t('err_401')}`;
    const body = e.body as XSecApiErrorBody | undefined;
    const detail = body?.error?.message || '';
    return `${e.status}${detail ? ` · ${detail}` : ''}`;
  }
  return t('action_error');
}

function fmtTime(iso?: string): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (isNaN(d.getTime()) || d.getUTCFullYear() < 2000) return '—';
  return d.toLocaleString();
}

interface Props {
  canWrite: boolean;
}

export function ParamsEditor({ canWrite }: Props) {
  const t = useTranslations('Xsec');

  const { data, isLoading, error, mutate } = useSWR<XSecParam[]>(
    'xsec:params',
    getXsecParams,
    { revalidateOnFocus: false, refreshInterval: 20_000 },
  );

  const [editingKey, setEditingKey] = useState<string | null>(null);
  const [draft, setDraft] = useState('');
  const [busyKey, setBusyKey] = useState<string | null>(null);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  function startEdit(p: XSecParam) {
    if (!canWrite) return;
    setEditingKey(p.key);
    setDraft(p.value);
    setMsg(null);
  }

  async function save(key: string) {
    if (!canWrite || busyKey) return;
    setBusyKey(key);
    setMsg(null);
    try {
      await putXsecParam(key, draft);
      setEditingKey(null);
      setMsg({ kind: 'ok', text: t('params_saved', { key }) });
      await mutate();
    } catch (e) {
      setMsg({ kind: 'err', text: errText(e, t) });
    } finally {
      setBusyKey(null);
    }
  }

  if (isLoading) {
    return <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)', padding: 16 }}>{t('loading')}</div>;
  }
  if (error) {
    return <div className="mono" style={{ fontSize: 12, color: 'var(--down)', padding: 16 }}>{t('load_error')}</div>;
  }
  if (!data || data.length === 0) {
    return <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)', padding: 16 }}>{t('no_params')}</div>;
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
      <div style={{ overflowX: 'auto' }}>
        <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
          <thead>
            <tr style={{ textAlign: 'left', color: 'var(--tx-faint)', fontSize: 10, textTransform: 'uppercase' }}>
              <th style={{ padding: '4px 8px' }}>{t('col_key')}</th>
              <th style={{ padding: '4px 8px' }}>{t('col_value')}</th>
              <th style={{ padding: '4px 8px' }}>{t('col_updated_at')}</th>
              <th style={{ padding: '4px 8px', textAlign: 'right' }}>{t('col_actions')}</th>
            </tr>
          </thead>
          <tbody>
            {data.map((p) => {
              const isEditing = editingKey === p.key;
              return (
                <tr key={p.key} style={{ borderTop: '1px solid var(--line)' }}>
                  <td className="mono" style={{ padding: '6px 8px', color: 'var(--tx-hi)' }}>{p.key}</td>
                  <td style={{ padding: '6px 8px' }}>
                    {isEditing ? (
                      <input
                        value={draft}
                        onChange={(e) => setDraft(e.target.value)}
                        disabled={busyKey === p.key} suppressHydrationWarning
                        style={{
                          padding: '5px 8px', borderRadius: 4, border: '1px solid var(--brand)',
                          background: 'var(--bg-void)', color: 'var(--tx-hi)', fontSize: 12, width: '100%',
                        }}
                      />
                    ) : (
                      <span className="mono" style={{ color: 'var(--tx-dim)' }}>{p.value}</span>
                    )}
                  </td>
                  <td className="mono" style={{ padding: '6px 8px', color: 'var(--tx-faint)' }}>{fmtTime(p.updated_at)}</td>
                  <td style={{ padding: '6px 8px', textAlign: 'right' }}>
                    {isEditing ? (
                      <div style={{ display: 'flex', gap: 6, justifyContent: 'flex-end' }}>
                        <button
                          type="button"
                          disabled={busyKey === p.key} suppressHydrationWarning
                          onClick={() => save(p.key)}
                          style={{
                            padding: '4px 10px', borderRadius: 4, border: 'none',
                            background: 'var(--brand)', color: 'var(--bg)', fontSize: 11, fontWeight: 600,
                            cursor: busyKey === p.key ? 'wait' : 'pointer',
                          }}
                        >
                          {busyKey === p.key ? t('working') : t('save')}
                        </button>
                        <button
                          type="button"
                          disabled={busyKey === p.key} suppressHydrationWarning
                          onClick={() => setEditingKey(null)}
                          style={{ padding: '4px 10px', borderRadius: 4, border: '1px solid var(--line)', background: 'transparent', color: 'var(--tx-dim)', fontSize: 11, cursor: 'pointer' }}
                        >
                          {t('cancel')}
                        </button>
                      </div>
                    ) : (
                      <button
                        type="button"
                        disabled={!canWrite} suppressHydrationWarning
                        onClick={() => startEdit(p)}
                        style={{
                          padding: '4px 10px', borderRadius: 4, border: '1px solid var(--line)',
                          background: 'transparent', color: canWrite ? 'var(--tx-hi)' : 'var(--tx-faint)',
                          fontSize: 11, cursor: canWrite ? 'pointer' : 'not-allowed',
                        }}
                      >
                        {t('edit')}
                      </button>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
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
