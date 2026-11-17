'use client';

// C3.3 — Tribunal (arbiter): real appeals list + create an appeal.
//
// Reads GET /api/v1/appeals (open + all non-deleted appeals, newest-first;
// empty today — the backend table starts with no seed). An appeal is created
// via POST /api/v1/appeals { spec_id, reason }. No invented content: empty
// queue renders an honest empty-state.

import { useState } from 'react';
import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { fetchAppeals, createAppeal, ApiError } from '@/lib/api';
import { LockedScreen } from '@/components/common/LockedScreen';
import { Panel, PanelHeader, PanelBody } from '@/components/ui/Panel';
import { Badge } from '@/components/ui/Badge';
import { Spinner } from '@/components/ui/Spinner';
import type { Appeal } from '@/lib/adapters';

const APPEALS_KEY = 'tribunal:appeals';

export default function TribunalPage() {
  const t = useTranslations('PageTribunal');
  const { data: appeals, error, isLoading, mutate } = useSWR<Appeal[]>(
    APPEALS_KEY,
    fetchAppeals,
    { refreshInterval: 30_000 },
  );

  const list = appeals ?? [];

  return (
    <div className="content-pad enter" style={{ maxWidth: 1000, margin: '0 auto' }}>
      <PageHeader />

      <LockedScreen screen="tribunal" bannerOnly>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          <NewAppealPanel onCreated={() => mutate()} />

          <Panel>
            <PanelHeader title={t('appeals_title')} sub={t('appeals_sub')}>
              <Badge variant="brand">{t('appeals_count', { count: list.length })}</Badge>
            </PanelHeader>
            <PanelBody>
              {isLoading && (
                <div style={{ display: 'flex', justifyContent: 'center', padding: 24 }}>
                  <Spinner size={20} />
                </div>
              )}

              {error && !isLoading && <ErrorState onRetry={() => mutate()} />}

              {!isLoading && !error && list.length === 0 && <EmptyState />}

              {!isLoading && !error && list.length > 0 && (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                  {list.map((a) => (
                    <AppealRow key={a.id} appeal={a} />
                  ))}
                </div>
              )}
            </PanelBody>
          </Panel>
        </div>
      </LockedScreen>
    </div>
  );
}

function PageHeader() {
  const t = useTranslations('PageTribunal');
  return (
    <div style={{ marginBottom: 18 }}>
      <div
        className="mono"
        style={{
          fontSize: 10,
          letterSpacing: '.08em',
          color: 'var(--brand)',
          textTransform: 'uppercase',
          marginBottom: 4,
        }}
      >
        {t('eyebrow')}
      </div>
      <h2 style={{ margin: 0, fontSize: 20, color: 'var(--tx-hi)', fontWeight: 700 }}>
        {t('title')}
      </h2>
      <p style={{ margin: '6px 0 0', fontSize: 12.5, color: 'var(--tx-dim)', maxWidth: 640 }}>
        {t('intro')}
      </p>
    </div>
  );
}

function NewAppealPanel({ onCreated }: { onCreated: () => void }) {
  const t = useTranslations('PageTribunal');
  const [specId, setSpecId] = useState('');
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [feedback, setFeedback] = useState<{ ok: boolean; msg: string } | null>(null);

  async function handleCreate() {
    const sid = specId.trim();
    const rsn = reason.trim();
    if (!sid || !rsn) {
      setFeedback({ ok: false, msg: t('both_required') });
      return;
    }
    setBusy(true);
    setFeedback(null);
    try {
      const res = await createAppeal(sid, rsn);
      setFeedback({ ok: true, msg: t('opened', { id: res.id.slice(0, 8) }) });
      setSpecId('');
      setReason('');
      onCreated();
    } catch (e) {
      let msg = t('open_failed');
      if (e instanceof ApiError) msg = t('error_status', { status: e.status });
      else if (e instanceof Error) msg = e.message;
      setFeedback({ ok: false, msg });
    } finally {
      setBusy(false);
    }
  }

  return (
    <Panel>
      <PanelHeader title={t('new_title')} sub={t('new_sub')} />
      <PanelBody>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          <input
            type="text"
            value={specId}
            onChange={(e) => setSpecId(e.target.value)}
            placeholder={t('spec_placeholder')}
            style={inputStyle}
          />
          <textarea
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            placeholder={t('reason_placeholder')}
            rows={3}
            style={{ ...inputStyle, resize: 'vertical' }}
          />
          <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
            <button
              type="button"
              onClick={handleCreate}
              disabled={busy}
              style={{
                fontFamily: 'var(--sans)',
                fontSize: 12,
                fontWeight: 600,
                padding: '8px 14px',
                borderRadius: 'var(--r)',
                border: '1px solid var(--brand)',
                background: 'var(--brand)',
                color: '#0b0d12',
                cursor: busy ? 'not-allowed' : 'pointer',
                opacity: busy ? 0.5 : 1,
              }}
            >
              {busy ? t('opening') : t('open_btn')}
            </button>
            {feedback && (
              <span
                className="mono"
                style={{ fontSize: 10.5, color: feedback.ok ? 'var(--up)' : 'var(--down, var(--tx-dim))' }}
              >
                {feedback.msg}
              </span>
            )}
          </div>
        </div>
      </PanelBody>
    </Panel>
  );
}

function AppealRow({ appeal }: { appeal: Appeal }) {
  const t = useTranslations('PageTribunal');
  return (
    <div className="panel-inset" style={{ padding: '12px 14px' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap', marginBottom: 6 }}>
        <span style={{ fontWeight: 600, color: 'var(--tx-hi)', fontSize: 13.5 }}>
          {t('spec_label', { id: appeal.specId || '—' })}
        </span>
        <Badge variant={appeal.status === 'open' ? 'brand' : 'neutral'}>
          {appeal.status || 'open'}
        </Badge>
        {appeal.createdAt && (
          <span className="mono" style={{ fontSize: 10.5, color: 'var(--tx-faint)' }}>
            {appeal.createdAt}
          </span>
        )}
      </div>
      {appeal.reason && (
        <div style={{ fontSize: 12.5, color: 'var(--tx-dim)' }}>{appeal.reason}</div>
      )}
      {appeal.appellantId && (
        <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', marginTop: 4 }}>
          {t('appellant', { id: appeal.appellantId })}
        </div>
      )}
    </div>
  );
}

function EmptyState() {
  const t = useTranslations('PageTribunal');
  return (
    <div style={{ textAlign: 'center', padding: '28px 12px', color: 'var(--tx-dim)' }}>
      <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--tx)' }}>{t('empty_title')}</div>
      <div style={{ fontSize: 12, marginTop: 6 }}>
        {t('empty_body')}
      </div>
    </div>
  );
}

function ErrorState({ onRetry }: { onRetry: () => void }) {
  const t = useTranslations('PageTribunal');
  return (
    <div style={{ textAlign: 'center', padding: '24px 12px' }}>
      <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--down, var(--tx-hi))' }}>
        {t('error_title')}
      </div>
      <div style={{ fontSize: 12, marginTop: 6, color: 'var(--tx-dim)' }}>
        {t('error_body')}
      </div>
      <button
        type="button"
        onClick={onRetry}
        style={{
          fontFamily: 'var(--sans)',
          fontSize: 12,
          fontWeight: 600,
          padding: '7px 12px',
          borderRadius: 'var(--r)',
          border: '1px solid var(--line-2)',
          background: 'transparent',
          color: 'var(--tx)',
          cursor: 'pointer',
          marginTop: 12,
        }}
      >
        {t('retry')}
      </button>
    </div>
  );
}

const inputStyle: React.CSSProperties = {
  fontFamily: 'var(--sans)',
  fontSize: 12.5,
  padding: '8px 10px',
  borderRadius: 'var(--r)',
  border: '1px solid var(--line-2)',
  background: 'var(--bg-inset)',
  color: 'var(--tx)',
};
