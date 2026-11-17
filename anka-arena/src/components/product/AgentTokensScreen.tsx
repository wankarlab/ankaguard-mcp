'use client';

// /agent-tokens — create (name, profile, expiry), show the value once, list
// without values, revoke behind an inline confirmation. Creating a `live`
// token needs a Pro license: the screen explains it and sends nothing.

import { useReducer, useState } from 'react';
import useSWR from 'swr';
import { Link } from '@/i18n/routing';
import { useTranslations } from 'next-intl';
import { Panel, PanelHeader, PanelBody } from '@/components/ui/Panel';
import { Spinner } from '@/components/ui/Spinner';
import {
  DEFAULT_TOKEN_DAYS,
  MAX_TOKEN_DAYS,
  TOKEN_PROFILES,
  apiErrorCode,
  createAgentToken,
  describeApiError,
  listAgentTokens,
  liveTokenBlocked,
  reduceRevoke,
  revokeAgentToken,
  validateTokenForm,
  type AgentToken,
  type TokenProfile,
} from '@/lib/product-api';
import { useLicense } from './hooks';
import { TokenCreatedNotice, TokenList } from './TokenParts';
import { Notice, PageHeader, inputStyle, labelStyle } from './shared';

export function AgentTokensScreen() {
  const t = useTranslations('Product');
  const { data: license } = useLicense();
  const { data, error, isLoading, mutate } = useSWR<AgentToken[]>('product:tokens', listAgentTokens, {
    revalidateOnFocus: false,
    shouldRetryOnError: false,
  });

  const [name, setName] = useState('');
  const [profile, setProfile] = useState<TokenProfile>('read');
  const [days, setDays] = useState(String(DEFAULT_TOKEN_DAYS));
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [licenseNeeded, setLicenseNeeded] = useState(false);
  const [created, setCreated] = useState<string | null>(null);
  const [revoke, dispatch] = useReducer(reduceRevoke, { pendingId: null });
  const [revokeBusy, setRevokeBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);

  // Known not-Pro: explain before sending. Unknown (license fetch failed): let the server decide.
  const liveBlocked = liveTokenBlocked(profile, license);
  const showLiveNotice = licenseNeeded || liveBlocked;

  async function create(e: React.FormEvent) {
    e.preventDefault();
    if (busy) return;
    setFormError(null);
    setLicenseNeeded(false);
    const invalid = validateTokenForm({ name, profile, days });
    if (invalid) {
      setFormError(t(`tok_err_${invalid}`, { max: MAX_TOKEN_DAYS }));
      return;
    }
    if (liveBlocked) {
      setLicenseNeeded(true);
      return;
    }
    setBusy(true);
    try {
      const res = await createAgentToken({ name: name.trim(), profile, expires_in_days: parseInt(days, 10) });
      setCreated(res.token);
      setName('');
      await mutate();
    } catch (err) {
      if (apiErrorCode(err) === 'LICENSE_REQUIRED') setLicenseNeeded(true);
      else setFormError(describeApiError(err, t('tok_create_error')));
    } finally {
      setBusy(false);
    }
  }

  async function confirmRevoke(id: string) {
    setRevokeBusy(true);
    setActionError(null);
    try {
      await revokeAgentToken(id);
      dispatch({ type: 'done' });
      await mutate();
    } catch (err) {
      dispatch({ type: 'cancel' });
      setActionError(describeApiError(err, t('tok_revoke_error')));
    } finally {
      setRevokeBusy(false);
    }
  }

  return (
    <div className="content-pad" style={{ maxWidth: 900, margin: '0 auto' }}>
      <PageHeader eyebrow={t('tok_eyebrow')} title={t('tok_title')} />
      <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
        {created && <TokenCreatedNotice value={created} onDismiss={() => setCreated(null)} />}

        <Panel>
          <PanelHeader title={t('tok_new_title')} sub={t('tok_new_sub')} />
          <PanelBody>
            <form onSubmit={create} style={{ display: 'grid', gap: 12, gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 200px), 1fr))', alignItems: 'end' }}>
              <label>
                <span className="mono" style={labelStyle}>{t('tok_name')}</span>
                <input value={name} onChange={(e) => setName(e.target.value)} maxLength={64} style={inputStyle} autoComplete="off" />
              </label>
              <label>
                <span className="mono" style={labelStyle}>{t('tok_profile')}</span>
                <select value={profile} onChange={(e) => setProfile(e.target.value as TokenProfile)} style={inputStyle}>
                  {TOKEN_PROFILES.map((p) => (
                    <option key={p} value={p}>{t(`tok_profile_${p}`)}</option>
                  ))}
                </select>
              </label>
              <label>
                <span className="mono" style={labelStyle}>{t('tok_days', { max: MAX_TOKEN_DAYS })}</span>
                <input
                  value={days}
                  onChange={(e) => setDays(e.target.value)}
                  inputMode="numeric"
                  style={inputStyle}
                  aria-describedby="tok-days-hint"
                />
              </label>
              <button type="submit" className="btn sm primary" disabled={busy} style={{ fontSize: 12, height: 34 }}>
                {busy ? <Spinner size={12} color="#fff" /> : t('tok_create')}
              </button>
            </form>
            <div id="tok-days-hint" className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 8 }}>
              {t('tok_profile_hint')}
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 10 }}>
              {showLiveNotice && (
                <Notice tone="warn" title={t('tok_live_license_title')}>
                  {t('tok_live_license_text')}{' '}
                  <Link href="/license" style={{ color: 'var(--brand)' }}>{t('tok_live_license_link')}</Link>
                </Notice>
              )}
              {formError && <Notice tone="error">{formError}</Notice>}
            </div>
          </PanelBody>
        </Panel>

        <Panel>
          <PanelHeader title={t('tok_list_title')} />
          <PanelBody>
            {actionError && <div style={{ marginBottom: 10 }}><Notice tone="error">{actionError}</Notice></div>}
            {isLoading && !data && <div className="mono" style={{ fontSize: 12, color: 'var(--tx-faint)' }}>{t('loading')}</div>}
            {error && !data && <Notice tone="error">{describeApiError(error, t('unavailable'))}</Notice>}
            {data && (
              <TokenList
                tokens={data}
                pendingId={revoke.pendingId}
                busy={revokeBusy}
                onAsk={(id) => dispatch({ type: 'ask', id })}
                onConfirm={confirmRevoke}
                onCancel={() => dispatch({ type: 'cancel' })}
              />
            )}
          </PanelBody>
        </Panel>
      </div>
    </div>
  );
}
