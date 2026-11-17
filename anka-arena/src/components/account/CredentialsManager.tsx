'use client';

// CredentialsManager — exchange API-key management shared by /account/keys and
// the Gremio CopySetup modal (D9). Exports:
//   - useExchangeCredentials: state + actions (list, select, add, verify)
//   - ExchangeSelect / AddKeyForm: the building blocks CopySetup composes
//     (they keep the existing UiGremio strings so CopySetup is unchanged)
//   - CredentialsManager: the full screen body (list without secrets, add,
//     test, delete behind ConfirmInline), strings under AccountKeys.

import type { CSSProperties } from 'react';
import { useCallback, useEffect, useState } from 'react';
import { useTranslations } from 'next-intl';
import { isProductEdition, supportedExchangesFor } from '@/lib/productMode';
import {
  ApiError,
  addCredential,
  deleteCredential,
  fetchCredentials,
  fetchSupportedExchanges,
  testCredential,
  type CredentialTestResult,
  type ExchangeCredential,
  type SupportedExchange,
} from '@/lib/api';
import { formatLastUsed } from '@/lib/format-date';
import { Badge } from '@/components/ui/Badge';
import { Spinner } from '@/components/ui/Spinner';
import { ConfirmInline } from '@/components/ui/ConfirmInline';

// ---- Hook ------------------------------------------------------------------

interface UseCredentialsOptions {
  /** Run the real permission test whenever the selected exchange has a key. Default true. */
  autoVerify?: boolean;
  /** Called right before an add-key request starts. */
  onAddStart?: () => void;
  /** Called when the add-key request fails. */
  onAddError?: (err: unknown) => void;
}

export function useExchangeCredentials(opts: UseCredentialsOptions = {}) {
  const { autoVerify = true, onAddStart, onAddError } = opts;

  const [exchanges, setExchanges] = useState<SupportedExchange[]>([]);
  const [credentials, setCredentials] = useState<ExchangeCredential[]>([]);
  const [credsLoading, setCredsLoading] = useState(true);
  const [loadError, setLoadError] = useState(false);
  const [selectedExchange, setSelectedExchange] = useState('');

  const [apiKey, setApiKey] = useState('');
  const [apiSecret, setApiSecret] = useState('');
  const [passphrase, setPassphrase] = useState('');
  const [addingKey, setAddingKey] = useState(false);

  const [verifyResult, setVerifyResult] = useState<CredentialTestResult | null>(null);
  const [verifying, setVerifying] = useState(false);

  const loadCredentials = useCallback(async () => {
    setCredsLoading(true);
    setLoadError(false);
    try {
      let failed = false;
      const [ex, creds] = await Promise.all([
        fetchSupportedExchanges().catch(() => { failed = true; return [] as SupportedExchange[]; }),
        fetchCredentials().catch(() => { failed = true; return [] as ExchangeCredential[]; }),
      ]);
      const shown = supportedExchangesFor(isProductEdition(), ex);
      setExchanges(shown);
      setCredentials(creds);
      setLoadError(failed);
      if (!selectedExchange && creds.length > 0) {
        setSelectedExchange(creds[0].exchange);
      } else if (!selectedExchange && isProductEdition() && shown.length === 1) {
        setSelectedExchange(shown[0].name); // single exchange: nothing to choose
      }
    } finally {
      setCredsLoading(false);
    }
  }, [selectedExchange]);

  useEffect(() => {
    void loadCredentials();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const selectedExchangeMeta = exchanges.find((e) => e.name === selectedExchange);
  const existingCred = credentials.find((c) => c.exchange === selectedExchange);
  const needsPassphrase = selectedExchangeMeta?.requires_passphrase ?? false;

  const runVerify = useCallback(async (exchange: string) => {
    if (!exchange) return;
    setVerifying(true);
    setVerifyResult(null);
    try {
      setVerifyResult(await testCredential(exchange));
    } catch {
      setVerifyResult(null);
    } finally {
      setVerifying(false);
    }
  }, []);

  useEffect(() => {
    if (!autoVerify) return;
    if (existingCred) {
      void runVerify(selectedExchange);
    } else {
      setVerifyResult(null);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedExchange, existingCred?.exchange]);

  function selectExchange(name: string) {
    setSelectedExchange(name);
    setVerifyResult(null);
  }

  async function handleAddKey() {
    if (!selectedExchange || !apiKey.trim() || !apiSecret.trim()) return;
    setAddingKey(true);
    onAddStart?.();
    try {
      await addCredential({
        exchange: selectedExchange,
        apiKey: apiKey.trim(),
        apiSecret: apiSecret.trim(),
        passphrase: needsPassphrase ? passphrase.trim() : undefined,
      });
      setApiKey('');
      setApiSecret('');
      setPassphrase('');
      await loadCredentials();
      if (autoVerify) await runVerify(selectedExchange);
    } catch (err) {
      onAddError?.(err);
    } finally {
      setAddingKey(false);
    }
  }

  return {
    exchanges, credentials, credsLoading, loadError, loadCredentials,
    selectedExchange, selectedExchangeMeta, selectExchange, existingCred, needsPassphrase,
    apiKey, setApiKey, apiSecret, setApiSecret, passphrase, setPassphrase,
    addingKey, handleAddKey,
    verifyResult, verifying, runVerify, setVerifyResult,
  };
}

export type ExchangeCredentialsState = ReturnType<typeof useExchangeCredentials>;

// ---- Shared styles -----------------------------------------------------------

export const credentialInputStyle: CSSProperties = {
  width: '100%',
  background: 'var(--bg)',
  border: '1px solid var(--line-2)',
  borderRadius: 'var(--r)',
  color: 'var(--tx)',
  padding: '7px 10px',
  fontFamily: 'var(--mono)',
  fontSize: 11.5,
  outline: 'none',
};

// ---- Building blocks (UiGremio strings, as in the original CopySetup) --------

export function ExchangeSelect({ state, id = 'copy-exchange' }: { state: ExchangeCredentialsState; id?: string }) {
  const t = useTranslations('UiGremio');
  const { credsLoading, selectedExchange, selectExchange, exchanges, credentials } = state;
  return (
    <div>
      <label
        htmlFor={id}
        className="mono"
        style={{ fontSize: 10.5, color: 'var(--tx-faint)', textTransform: 'uppercase', letterSpacing: '0.06em', display: 'block', marginBottom: 6 }}
      >
        {t('exchange_label')}
      </label>
      {credsLoading ? (
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, color: 'var(--tx-dim)' }}>
          <Spinner size={14} /><span className="mono" style={{ fontSize: 11 }}>{t('loading_exchanges')}</span>
        </div>
      ) : (
        <select
          id={id}
          value={selectedExchange}
          onChange={(e) => selectExchange(e.target.value)}
          style={{
            width: '100%',
            background: 'var(--bg-inset)',
            border: '1px solid var(--line-2)',
            borderRadius: 'var(--r)',
            color: 'var(--tx)',
            padding: '8px 10px',
            fontFamily: 'var(--mono)',
            fontSize: 12,
            outline: 'none',
            cursor: 'pointer',
          }}
        >
          <option value="">{t('select_exchange')}</option>
          {exchanges.map((ex) => {
            const connected = credentials.some((c) => c.exchange === ex.name);
            return (
              <option key={ex.name} value={ex.name}>
                {ex.display_name}{connected ? t('connected_suffix') : t('no_key_suffix')}
              </option>
            );
          })}
        </select>
      )}
    </div>
  );
}

/** Add-key form; renders nothing unless the selected exchange has no credential yet. */
export function AddKeyForm({ state }: { state: ExchangeCredentialsState }) {
  const t = useTranslations('UiGremio');
  const {
    selectedExchange, existingCred, credsLoading, selectedExchangeMeta, needsPassphrase,
    apiKey, setApiKey, apiSecret, setApiSecret, passphrase, setPassphrase, addingKey, handleAddKey,
  } = state;
  if (!selectedExchange || existingCred || credsLoading) return null;
  return (
    <div className="panel-inset" style={{ padding: 12, display: 'flex', flexDirection: 'column', gap: 8 }}>
      <b className="hi" style={{ fontSize: 11.5 }}>
        {t('connect_key', { exchange: selectedExchangeMeta?.display_name ?? selectedExchange })}
      </b>
      <input type="text" placeholder="API key" value={apiKey} onChange={(e) => setApiKey(e.target.value)} style={credentialInputStyle} />
      <input type="password" placeholder="API secret" value={apiSecret} onChange={(e) => setApiSecret(e.target.value)} style={credentialInputStyle} />
      {needsPassphrase && (
        <input type="password" placeholder="Passphrase" value={passphrase} onChange={(e) => setPassphrase(e.target.value)} style={credentialInputStyle} />
      )}
      <button
        className="btn sm primary"
        onClick={handleAddKey}
        disabled={addingKey || !apiKey.trim() || !apiSecret.trim()}
        style={{ alignSelf: 'flex-start', fontSize: 11 }}
      >
        {addingKey ? <Spinner size={12} color="#fff" /> : t('connect_verify')}
      </button>
    </div>
  );
}

// ---- Full manager (/account/keys) ---------------------------------------------

type RowTest = { state: 'running' } | { state: 'done'; result: CredentialTestResult } | { state: 'error'; message: string };

function errMessage(err: unknown, fallback: string): string {
  if (err instanceof ApiError) {
    const body = err.body as { error?: { message?: string } } | undefined;
    const detail = body?.error?.message;
    return `${err.status}${detail ? ` · ${detail}` : ''}`;
  }
  return fallback;
}

export function CredentialsManager() {
  const t = useTranslations('AccountKeys');
  const [addError, setAddError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [tests, setTests] = useState<Record<string, RowTest>>({});
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [deleting, setDeleting] = useState(false);

  const state = useExchangeCredentials({
    autoVerify: false,
    onAddStart: () => setAddError(null),
    onAddError: (err) => setAddError(errMessage(err, t('network_error'))),
  });
  const { credentials, credsLoading, loadError, loadCredentials, exchanges } = state;

  async function runTest(exchange: string) {
    setTests((prev) => ({ ...prev, [exchange]: { state: 'running' } }));
    try {
      const result = await testCredential(exchange);
      setTests((prev) => ({ ...prev, [exchange]: { state: 'done', result } }));
    } catch (err) {
      setTests((prev) => ({ ...prev, [exchange]: { state: 'error', message: errMessage(err, t('network_error')) } }));
    }
  }

  async function runDelete(exchange: string) {
    setDeleting(true);
    setActionError(null);
    try {
      await deleteCredential(exchange);
      setConfirmDelete(null);
      setTests((prev) => {
        const next = { ...prev };
        delete next[exchange];
        return next;
      });
      await loadCredentials();
    } catch (err) {
      setActionError(errMessage(err, t('network_error')));
    } finally {
      setDeleting(false);
    }
  }

  const displayName = (ex: string) => exchanges.find((e) => e.name === ex)?.display_name ?? ex;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div className="disclaimer">
        <span>{t('security_note')}</span>
      </div>

      {credsLoading && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, color: 'var(--tx-dim)' }}>
          <Spinner size={14} /><span className="mono" style={{ fontSize: 11 }}>{t('loading')}</span>
        </div>
      )}

      {!credsLoading && loadError && (
        <div className="mono" style={{ fontSize: 12, color: 'var(--down)' }}>{t('load_error')}</div>
      )}

      {!credsLoading && !loadError && credentials.length === 0 && (
        <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{t('empty')}</div>
      )}

      {credentials.length > 0 && (
        <div style={{ overflowX: 'auto' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
            <thead>
              <tr style={{ textAlign: 'left', color: 'var(--tx-faint)', fontSize: 10, textTransform: 'uppercase' }}>
                <th style={{ padding: '4px 8px' }}>{t('col_exchange')}</th>
                <th style={{ padding: '4px 8px' }}>{t('col_key')}</th>
                <th style={{ padding: '4px 8px' }}>{t('col_status')}</th>
                <th style={{ padding: '4px 8px' }}>{t('col_last_used')}</th>
                <th style={{ padding: '4px 8px', textAlign: 'right' }}>{t('col_actions')}</th>
              </tr>
            </thead>
            <tbody>
              {credentials.map((c) => {
                const test = tests[c.exchange];
                return (
                  <tr key={c.exchange} style={{ borderTop: '1px solid var(--line)', verticalAlign: 'top' }}>
                    <td style={{ padding: '8px', color: 'var(--tx-hi)' }}>
                      {displayName(c.exchange)}
                      {c.sandbox && <Badge variant="neutral" style={{ marginLeft: 6, fontSize: 9 }}>sandbox</Badge>}
                    </td>
                    <td className="mono" style={{ padding: '8px', color: 'var(--tx-dim)' }}>{c.api_key_hint || '—'}</td>
                    <td style={{ padding: '8px' }}>
                      <Badge variant={c.status === 'active' ? 'ok' : 'neutral'} style={{ fontSize: 9 }}>{c.status || '—'}</Badge>
                    </td>
                    <td className="mono" style={{ padding: '8px', color: 'var(--tx-faint)' }}>
                      {formatLastUsed(c.last_used_at)}
                    </td>
                    <td style={{ padding: '8px', textAlign: 'right' }}>
                      <div style={{ display: 'flex', gap: 6, justifyContent: 'flex-end' }}>
                        <button
                          type="button"
                          className="btn sm"
                          disabled={test?.state === 'running'}
                          onClick={() => void runTest(c.exchange)}
                          style={{ fontSize: 11 }}
                        >
                          {test?.state === 'running' ? <Spinner size={12} /> : t('test')}
                        </button>
                        <button
                          type="button"
                          className="btn sm"
                          onClick={() => setConfirmDelete(c.exchange)}
                          style={{ fontSize: 11, color: 'var(--down)' }}
                        >
                          {t('delete')}
                        </button>
                      </div>
                      {test?.state === 'done' && (
                        <div className="mono" style={{ fontSize: 10.5, marginTop: 6, textAlign: 'right' }}>
                          <span className={test.result.valid ? 'up' : 'down'}>
                            {test.result.valid ? t('test_ok') : (test.result.message || t('test_invalid'))}
                          </span>
                          {test.result.permissions.length > 0 && (
                            <span style={{ color: 'var(--tx-faint)' }}> · {test.result.permissions.join(', ')}</span>
                          )}
                          {test.result.permissions.some((p) => p.toLowerCase().includes('withdraw')) && (
                            <div className="down">{t('withdraw_warning')}</div>
                          )}
                        </div>
                      )}
                      {test?.state === 'error' && (
                        <div className="mono down" style={{ fontSize: 10.5, marginTop: 6, textAlign: 'right' }}>
                          {t('test_failed', { detail: test.message })}
                        </div>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {confirmDelete && (
        <ConfirmInline
          message={t('delete_confirm', { exchange: displayName(confirmDelete) })}
          confirmLabel={t('delete_confirm_yes')}
          cancelLabel={t('cancel')}
          busy={deleting}
          onConfirm={() => void runDelete(confirmDelete)}
          onCancel={() => setConfirmDelete(null)}
        />
      )}

      {actionError && (
        <div className="mono" style={{ fontSize: 12, color: 'var(--down)' }}>{t('action_error', { detail: actionError })}</div>
      )}

      <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
        <b className="hi" style={{ fontSize: 12 }}>{t('add_title')}</b>
        <ExchangeSelect state={state} id="keys-exchange" />
        <AddKeyForm state={state} />
        {state.existingCred && !state.credsLoading && (
          <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{t('already_connected')}</div>
        )}
        {addError && (
          <div className="mono" style={{ fontSize: 12, color: 'var(--down)' }}>{t('add_error', { detail: addError })}</div>
        )}
      </div>
    </div>
  );
}
