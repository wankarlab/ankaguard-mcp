'use client';

import { useState, useCallback } from 'react';
import { useTranslations } from 'next-intl';
import useSWR, { useSWRConfig } from 'swr';
import { api } from '@/lib/api';
import { getCurrentUser } from '@/lib/auth';
import { Badge } from '@/components/ui/Badge';
import { Spinner } from '@/components/ui/Spinner';
import { ConfirmInline } from '@/components/ui/ConfirmInline';
import { GuildTable, type GuildStrategy } from '@/components/gremio/GuildTable';
import { CopySetup } from '@/components/gremio/CopySetup';
import { FeeLedger } from '@/components/gremio/FeeLedger';
import { GuildTradeHistory } from '@/components/gremio/GuildTradeHistory';

// ---- Types ----------------------------------------------------------------

interface CopyStatus {
  active:     boolean;
  spec_id?:   string;
  pnl_usd?:  number;
  since?:     string;
}

// ---- Fetcher ---------------------------------------------------------------

const fetchCopyStatus = () => api.get<CopyStatus>('/api/v1/marketplace/copy/status');

// ---- CopyStatusBanner ------------------------------------------------------

// eslint-disable-next-line @typescript-eslint/no-unused-vars
function CopyStatusBanner({ userId }: { userId: string }) {
  const t = useTranslations('Gremio');
  const tc = useTranslations('Confirm');
  const { data, error, isLoading, mutate } = useSWR<CopyStatus>(
    '/api/v1/marketplace/copy/status',
    fetchCopyStatus,
    { refreshInterval: 30_000 },
  );

  const [disconnecting, setDisconnecting] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [disconnectError, setDisconnectError] = useState<string | null>(null);

  async function handleDisconnect() {
    setConfirming(false);
    setDisconnectError(null);
    setDisconnecting(true);
    try {
      await api.delete(`/api/v1/marketplace/copy/${encodeURIComponent(data?.spec_id ?? '')}`);
      mutate({ active: false });
    } catch (e) {
      const detail = e instanceof Error && e.message ? ` (${e.message})` : '';
      setDisconnectError(`${tc('disconnect_error')}${detail}`);
    } finally {
      setDisconnecting(false);
    }
  }

  if (isLoading || error || !data?.active) return null;

  const pnlColor = (data.pnl_usd ?? 0) >= 0 ? 'var(--up)' : 'var(--down)';

  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        flexWrap: 'wrap',
        gap: 12,
        padding: '10px 16px',
        marginBottom: 14,
        background: 'var(--up-soft)',
        border: '1px solid rgba(54,205,134,0.28)',
        borderRadius: 'var(--r)',
        fontSize: 12,
      }}
    >
      {/* Live dot */}
      <span
        className="live-dot"
        style={{
          display: 'inline-block',
          width: 8,
          height: 8,
          borderRadius: '50%',
          background: 'var(--up)',
          flexShrink: 0,
        }}
      />

      <span className="hi" style={{ fontWeight: 600 }}>{t('copy_active')}</span>
      <span className="mono dim" style={{ fontSize: 11 }}>{data.spec_id}</span>

      <span style={{ flex: 1 }} />

      {data.pnl_usd !== undefined && (
        <span className="mono" style={{ color: pnlColor, fontWeight: 600 }}>
          {data.pnl_usd >= 0 ? '+' : ''}${data.pnl_usd.toFixed(2)} PnL
        </span>
      )}

      <button
        className="btn danger sm"
        onClick={() => setConfirming(true)}
        disabled={disconnecting || confirming}
        style={{ fontSize: 11 }}
      >
        {disconnecting ? <Spinner size={12} color="var(--real)" /> : t('disconnect')}
      </button>

      {confirming && (
        <div style={{ flexBasis: '100%' }}>
          <ConfirmInline
            message={tc('disconnect')}
            confirmLabel={tc('disconnect_btn')}
            cancelLabel={tc('cancel')}
            busy={disconnecting}
            onCancel={() => setConfirming(false)}
            onConfirm={handleDisconnect}
          />
        </div>
      )}
      {disconnectError && (
        <div role="alert" className="mono" style={{ flexBasis: '100%', fontSize: 11, color: 'var(--down)' }}>
          {disconnectError}
        </div>
      )}
    </div>
  );
}

// ---- Page ------------------------------------------------------------------

export default function GremioPage() {
  const t = useTranslations('Gremio');
  const user = getCurrentUser();
  const userId = user?.user_id ?? '';

  const [copyTarget, setCopyTarget] = useState<GuildStrategy | null>(null);
  const { mutate } = useSWRConfig();

  const handleCopy = useCallback((strategy: GuildStrategy) => {
    setCopyTarget(strategy);
  }, []);

  const handleConnected = useCallback(() => {
    // Refresh copy status banner
    mutate('/api/v1/marketplace/copy/status');
  }, [mutate]);

  return (
    <div className="content-pad enter">
      <div className="maxw">

        {/* ── Page header ── */}
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            flexWrap: 'wrap',
            gap: 10,
            marginBottom: 14,
          }}
        >
          <div style={{ flex: 1 }}>
            <div className="eyebrow" style={{ color: 'var(--micro)' }}>
              {t('eyebrow')}
            </div>
            <h2 style={{ margin: '4px 0 0', fontSize: 17, color: 'var(--tx-hi)' }}>
              {t('title')}
            </h2>
          </div>
          <Badge variant="real">{t('badge')}</Badge>
        </div>

        {/* ── Legal disclaimer ── */}
        <div className="disclaimer legal" style={{ marginBottom: 16 }}>
          <span style={{ flex: 'none', fontSize: 16 }}>🛡</span>
          <span>
            <b>{t('disclaimer_strong')}</b> {t('disclaimer_body')}{' '}
            <b>{t('disclaimer_gated')}</b> {t('disclaimer_until')}
          </span>
        </div>

        {/* ── Copy status banner (only when active) ── */}
        {userId && <CopyStatusBanner userId={userId} />}

        {/* ── Main grid: guild table + right column ── */}
        <div
          style={{
            display: 'grid',
            gridTemplateColumns: 'minmax(0,1fr) 340px',
            gap: 16,
            alignItems: 'start',
          }}
        >

          {/* Left: guild table */}
          <div className="panel" style={{ overflow: 'hidden' }}>
            <div className="panel-h">
              <div style={{ flex: 1 }}>
                <h3>{t('guild_title')}</h3>
                <span className="sub">/api/v1/marketplace/strategies</span>
              </div>
            </div>
            <GuildTable onCopy={handleCopy} />
          </div>

          {/* Right column: copy info card + fee ledger */}
          <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>

            {/* Copy info card */}
            <div className="panel" style={{ overflow: 'hidden' }}>
              <div className="panel-h">
                <div style={{ flex: 1 }}>
                  <h3>{t('copy_trade')}</h3>
                  <span className="sub">{t('non_custodial')}</span>
                </div>
              </div>
              <div style={{ padding: 14, display: 'flex', flexDirection: 'column', gap: 10 }}>

                {/* Non-custodial box */}
                <div
                  className="panel-inset"
                  style={{ padding: 12, borderColor: 'rgba(54,205,134,.3)' }}
                >
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 7 }}>
                    <span style={{ fontSize: 15 }}>🔒</span>
                    <b className="hi" style={{ fontSize: 12 }}>{t('non_custodial_title')}</b>
                  </div>
                  <p
                    style={{
                      margin: 0,
                      fontSize: 11.5,
                      color: 'var(--tx-dim)',
                      lineHeight: 1.55,
                    }}
                  >
                    {t('non_custodial_body_1')} <b className="hi">{t('non_custodial_body_strong')}</b>{t('non_custodial_body_2')}{' '}
                    <span className="mono up">verifyNoWithdraw: block</span>.
                  </p>
                </div>

                {/* Instruction — fee economics live in the Fee Ledger below (backend-sourced) */}
                <p style={{ margin: 0, fontSize: 11, color: 'var(--tx-dim)', lineHeight: 1.5 }}>
                  {t('instruction_1')}{' '}
                  <b className="hi">{t('instruction_copy')}</b> {t('instruction_2')}{' '}
                  <b className="hi">{t('instruction_ledger')}</b>.
                </p>
              </div>
            </div>

            {/* Fee Ledger */}
            {userId ? (
              <FeeLedger userId={userId} />
            ) : (
              <div className="panel">
                <div className="panel-h">
                  <h3>{t('fee_ledger')}</h3>
                </div>
                <div style={{ padding: 14 }}>
                  <div className="gate-lock">
                    <span>🔐</span>
                    <span style={{ fontSize: 11.5 }}>{t('login_for_ledger')}</span>
                  </div>
                </div>
              </div>
            )}
          </div>
        </div>

        {/* ── Historical trades (anti-survivorship + explainability) ── */}
        <GuildTradeHistory />
      </div>

      {/* ── CopySetup modal ── */}
      {copyTarget && (
        <CopySetup
          strategy={copyTarget}
          onClose={() => setCopyTarget(null)}
          onConnected={() => {
            handleConnected();
            setCopyTarget(null);
          }}
        />
      )}
    </div>
  );
}
