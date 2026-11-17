'use client';

// CopySetup (C1.3 + C1.7) — non-custodial copy onboarding.
//
// C1.3 Consent gate: before any copy action the user MUST accept the versioned
// copy_trading consent (read from /consent/status; the legal text + acceptance
// come from the backend via the shared ConsentModal). Copy is blocked until
// accepted.
//
// C1.7 Non-custodial keys: the user connects their OWN exchange API keys
// (POST /api/v1/credentials), and we run the REAL test (POST .../test) whose
// `permissions` array is what we display next to the "Operamos TU cuenta con
// TUS llaves" banner — solo-trade is OK, withdraw access is flagged as unsafe.
// Nothing about fees/splits/keys is hardcoded.

import { useEffect, useState } from 'react';
import { useTranslations } from 'next-intl';
import { api, ApiError } from '@/lib/api';
import { useConsentStore } from '@/stores/consent';
import { ConsentModal } from '@/components/common/ConsentModal';
import { Badge } from '@/components/ui/Badge';
import { Spinner } from '@/components/ui/Spinner';
import { AddKeyForm, ExchangeSelect, useExchangeCredentials } from '@/components/account/CredentialsManager';
import type { GuildStrategy } from '@/components/gremio/GuildTable';

const CONSENT_KIND = 'copy_trading';

// ---- Types ----------------------------------------------------------------

interface CopySetupProps {
  strategy: GuildStrategy;
  onClose: () => void;
  onConnected: () => void;
}

type ConnectState = 'idle' | 'loading' | 'done' | 'error';

// A connected exchange key has withdraw access if its permissions include it.
function hasWithdraw(perms: string[]): boolean {
  return perms.some((p) => p.toLowerCase().includes('withdraw'));
}

// ---- Component ------------------------------------------------------------

export function CopySetup({ strategy, onClose, onConnected }: CopySetupProps) {
  const t = useTranslations('UiGremio');
  const tc = useTranslations('Confirm');
  // ── Consent (C1.3) ──────────────────────────────────────────────────────
  const refreshConsent  = useConsentStore((s) => s.refresh);
  const consentStatus   = useConsentStore((s) => s.statusByKind[CONSENT_KIND]);
  const [consentModalOpen, setConsentModalOpen] = useState(false);
  const consentAccepted = consentStatus?.accepted ?? false;

  useEffect(() => {
    void refreshConsent(CONSENT_KIND);
  }, [refreshConsent]);

  // ── Credentials (C1.7) ──────────────────────────────────────────────────
  const [connectState, setConnectState] = useState<ConnectState>('idle');
  const [errorMsg, setErrorMsg]         = useState<string | null>(null);
  const [legalBlock, setLegalBlock]     = useState(false);

  // Credentials state/logic lives in the shared CredentialsManager module (D9).
  const creds = useExchangeCredentials({
    onAddStart: () => setErrorMsg(null),
    onAddError: (err) =>
      setErrorMsg(
        err instanceof ApiError
          ? t('save_key_error', { status: err.status })
          : t('save_key_network_error'),
      ),
  });
  const { selectedExchange, existingCred, verifyResult, verifying } = creds;

  const withdrawUnsafe = verifyResult ? hasWithdraw(verifyResult.permissions) : false;

  const canCopy =
    consentAccepted &&
    !!existingCred &&
    !!verifyResult?.valid &&
    !withdrawUnsafe &&
    connectState !== 'loading';

  async function handleConnect() {
    if (!canCopy) return;
    setConnectState('loading');
    setErrorMsg(null);
    setLegalBlock(false);

    try {
      await api.post('/api/v1/marketplace/copy', {
        spec_id:  strategy.id,
        exchange: selectedExchange,
      });
      setConnectState('done');
      onConnected();
    } catch (err) {
      if (err instanceof ApiError) {
        if (err.status === 451) {
          setLegalBlock(true);
          setConnectState('error');
          setErrorMsg(
            t('jurisdiction_blocked'),
          );
        } else {
          setConnectState('error');
          setErrorMsg(t('connect_error', { status: err.status }));
        }
      } else {
        setConnectState('error');
        setErrorMsg(t('network_retry'));
      }
    }
  }

  return (
    <>
      {/* Consent gate modal (C1.3) — legal text + acceptance from backend */}
      <ConsentModal
        kind={CONSENT_KIND}
        open={consentModalOpen}
        title={t('consent_title')}
        onAccepted={() => {
          setConsentModalOpen(false);
          void refreshConsent(CONSENT_KIND);
        }}
        onCancel={() => setConsentModalOpen(false)}
      />

      {/* scrim */}
      <div
        className="modal-scrim"
        onClick={(e) => {
          if (e.target === e.currentTarget) onClose();
        }}
      >
        <div className="modal" style={{ width: 540 }}>
          {/* Header */}
          <div className="drawer-h" style={{ padding: '14px 18px' }}>
            <div style={{ flex: 1 }}>
              <h3 style={{ margin: 0, fontSize: 13, color: 'var(--tx-hi)' }}>
                {t('copy_setup')}
              </h3>
              <span className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)' }}>
                {strategy.id}
              </span>
            </div>
            <button
              className="btn ghost icon"
              onClick={onClose}
              aria-label={t('close')}
              style={{ fontSize: 18, color: 'var(--tx-dim)', lineHeight: 1 }}
            >
              ×
            </button>
          </div>

          {/* Body */}
          <div style={{ padding: 18, display: 'flex', flexDirection: 'column', gap: 14 }}>

            {/* Non-custodial banner + REAL verify result (C1.7) */}
            <div
              className="panel-inset"
              style={{
                padding: 12,
                borderColor: withdrawUnsafe ? 'rgba(240,86,79,.4)' : 'rgba(54,205,134,.3)',
                display: 'flex',
                flexDirection: 'column',
                gap: 8,
              }}
            >
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <span style={{ fontSize: 16 }}>🔒</span>
                <b className="hi" style={{ fontSize: 12 }}>{t('non_custodial_title')}</b>
              </div>
              <p style={{ margin: 0, fontSize: 11.5, color: 'var(--tx-dim)', lineHeight: 1.55 }}>
                {t('non_custodial_1')} <b className="hi">{t('non_custodial_strong')}</b>{t('non_custodial_2')}
              </p>

              {/* Live verify status */}
              {verifying && (
                <div style={{ display: 'flex', alignItems: 'center', gap: 7, fontSize: 11, color: 'var(--tx-dim)' }}>
                  <Spinner size={12} />
                  <span className="mono">{t('verifying_perms')}</span>
                </div>
              )}
              {!verifying && verifyResult && (
                <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, alignItems: 'center' }}>
                  {verifyResult.permissions.length === 0 && (
                    <span className="mono" style={{ fontSize: 10.5, color: 'var(--tx-faint)' }}>
                      {t('no_perms')}
                    </span>
                  )}
                  {verifyResult.permissions.map((p) => {
                    const isWithdraw = p.toLowerCase().includes('withdraw');
                    return (
                      <Badge key={p} variant={isWithdraw ? 'real' : 'ok'} style={{ fontSize: 9 }}>
                        {p}
                      </Badge>
                    );
                  })}
                  {withdrawUnsafe ? (
                    <span className="mono down" style={{ fontSize: 10.5 }}>
                      {t('withdraw_warning')}
                    </span>
                  ) : verifyResult.valid ? (
                    <span className="mono up" style={{ fontSize: 10.5 }}>
                      {t('trade_only_verified')}
                    </span>
                  ) : (
                    <span className="mono down" style={{ fontSize: 10.5 }}>
                      {verifyResult.message || t('invalid_key')}
                    </span>
                  )}
                </div>
              )}
            </div>

            {/* Legal disclaimer */}
            <div className="disclaimer legal">
              <span style={{ flex: 'none', fontSize: 16 }}>⚠</span>
              <span>
                <b>{t('legal_strong')}</b> {t('legal_body')}{' '}
                <b>legal-gated</b> {t('legal_until')}
              </span>
            </div>

            {/* Exchange selector (real connected keys) */}
            <ExchangeSelect state={creds} />

            {/* Add-key form when the selected exchange has no credential yet */}
            <AddKeyForm state={creds} />

            {/* Consent gate row (C1.3) */}
            <div
              className="panel-inset"
              style={{
                padding: '10px 14px',
                display: 'flex',
                alignItems: 'center',
                gap: 10,
                borderColor: consentAccepted ? 'rgba(54,205,134,.3)' : 'rgba(233,162,62,.4)',
              }}
            >
              <span style={{ fontSize: 14 }}>{consentAccepted ? '✓' : '📜'}</span>
              <span style={{ flex: 1, fontSize: 11.5, color: 'var(--tx-dim)', lineHeight: 1.5 }}>
                {consentAccepted ? (
                  <>{t('consent_accepted_1')} <b className="up">{t('consent_accepted_strong')}</b> (v{consentStatus?.acceptedVersion}).</>
                ) : (
                  <>{t('consent_required')}</>
                )}
              </span>
              {!consentAccepted && (
                <button
                  className="btn sm"
                  onClick={() => setConsentModalOpen(true)}
                  style={{ fontSize: 11 }}
                >
                  {t('read_accept')}
                </button>
              )}
            </div>

            {/* Copiers count (backend) */}
            <div className="panel-inset" style={{ padding: '10px 14px' }}>
              <dl className="kv" style={{ gridTemplateColumns: '1fr auto', gap: '7px 12px' }}>
                <dt>{t('current_copiers')}</dt>
                <dd>{strategy.copiers}</dd>
              </dl>
            </div>

            {/* Legal block message (451) */}
            {legalBlock && (
              <div className="disclaimer legal" style={{ flexDirection: 'column', gap: 6 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 7 }}>
                  <Badge variant="real">451 · Legal-gated</Badge>
                </div>
                <span>{errorMsg}</span>
              </div>
            )}

            {/* Generic error */}
            {errorMsg && !legalBlock && (
              <div className="disclaimer" style={{ borderColor: 'rgba(240,86,79,0.28)', background: 'var(--real-soft)', color: '#f3a9a4' }}>
                <span>{errorMsg}</span>
              </div>
            )}

            {/* Success */}
            {connectState === 'done' && (
              <div
                className="badge ok"
                style={{ width: '100%', justifyContent: 'center', padding: 10, borderRadius: 'var(--r)' }}
              >
                {t('copy_active_audited')}
              </div>
            )}

            {/* Actions */}
            {connectState !== 'done' && (
              <div style={{ display: 'flex', gap: 10, justifyContent: 'flex-end' }}>
                <button className="btn" onClick={onClose}>
                  {tc('cancel')}
                </button>
                <button
                  className="btn primary"
                  disabled={!canCopy}
                  onClick={handleConnect}
                  title={
                    !consentAccepted
                      ? t('hint_consent')
                      : !existingCred
                        ? t('hint_connect_key')
                        : withdrawUnsafe
                          ? t('hint_withdraw')
                          : !verifyResult?.valid
                            ? t('hint_verify_failed')
                            : undefined
                  }
                  style={{ minWidth: 130, opacity: canCopy ? 1 : 0.5, cursor: canCopy ? 'pointer' : 'not-allowed' }}
                >
                  {connectState === 'loading' ? (
                    <span style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                      <Spinner size={14} color="#fff" />
                      {t('connecting')}
                    </span>
                  ) : (
                    t('copy')
                  )}
                </button>
              </div>
            )}

            {connectState === 'done' && (
              <button className="btn" style={{ width: '100%' }} onClick={onClose}>
                {t('close')}
              </button>
            )}
          </div>
        </div>
      </div>
    </>
  );
}
