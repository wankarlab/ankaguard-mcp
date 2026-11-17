'use client';

// C3.2 — Cola Adversarial (reviewer): real adversarial review queue.
//
// Reads GET /api/v1/adversarial/queue (status='pending' items; empty today —
// the backend table starts with no seed). A reviewer flags an item with a
// reason via POST /api/v1/adversarial/:id/flag. No invented content: when the
// queue is empty we show an honest empty-state.

import { useState } from 'react';
import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { fetchAdversarialQueue, flagAdversarial, ApiError } from '@/lib/api';
import { LockedScreen } from '@/components/common/LockedScreen';
import { Panel, PanelHeader, PanelBody } from '@/components/ui/Panel';
import { Badge } from '@/components/ui/Badge';
import { Spinner } from '@/components/ui/Spinner';
import type { AdversarialItem } from '@/lib/adapters';

const QUEUE_KEY = 'adversarial:queue';

export default function AdversarialPage() {
  const t = useTranslations('PageAdversarial');
  const { data: items, error, isLoading, mutate } = useSWR<AdversarialItem[]>(
    QUEUE_KEY,
    fetchAdversarialQueue,
    { refreshInterval: 20_000 },
  );

  const list = items ?? [];

  return (
    <div className="content-pad enter" style={{ maxWidth: 1000, margin: '0 auto' }}>
      <PageHeader />

      <LockedScreen screen="adversarial" bannerOnly>
        <Panel>
          <PanelHeader
            title={t('queue_title')}
            sub={t('queue_sub')}
          >
            <Badge variant="brand">{t('in_queue', { count: list.length })}</Badge>
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
                {list.map((item) => (
                  <ItemRow key={item.id} item={item} onFlagged={() => mutate()} />
                ))}
              </div>
            )}
          </PanelBody>
        </Panel>
      </LockedScreen>
    </div>
  );
}

function PageHeader() {
  const t = useTranslations('PageAdversarial');
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

function ItemRow({ item, onFlagged }: { item: AdversarialItem; onFlagged: () => void }) {
  const t = useTranslations('PageAdversarial');
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [feedback, setFeedback] = useState<{ ok: boolean; msg: string } | null>(null);

  async function handleFlag() {
    const trimmed = reason.trim();
    if (!trimmed) {
      setFeedback({ ok: false, msg: t('reason_required') });
      return;
    }
    setBusy(true);
    setFeedback(null);
    try {
      const res = await flagAdversarial(item.id, trimmed);
      setFeedback({ ok: true, msg: t('flagged', { id: res.flag_id.slice(0, 8) }) });
      setReason('');
      onFlagged();
    } catch (e) {
      let msg = t('flag_failed');
      if (e instanceof ApiError) {
        const code = (e.body as { error?: { code?: string } })?.error?.code;
        msg = code === 'NOT_FOUND' ? t('not_found') : t('error_status', { status: e.status });
      } else if (e instanceof Error) {
        msg = e.message;
      }
      setFeedback({ ok: false, msg });
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="panel-inset" style={{ padding: '12px 14px' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap', marginBottom: 10 }}>
        <span style={{ fontWeight: 600, color: 'var(--tx-hi)', fontSize: 13.5 }}>
          {t('spec_label', { id: item.specId || item.id })}
        </span>
        <Badge variant="micro">{item.status || 'pending'}</Badge>
        {item.createdAt && (
          <span className="mono" style={{ fontSize: 10.5, color: 'var(--tx-faint)' }}>
            {item.createdAt}
          </span>
        )}
      </div>

      <div style={{ display: 'flex', gap: 8, alignItems: 'flex-start', flexWrap: 'wrap' }}>
        <input
          type="text"
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder={t('reason_placeholder')}
          style={{
            flex: 1,
            minWidth: 220,
            fontFamily: 'var(--sans)',
            fontSize: 12.5,
            padding: '7px 10px',
            borderRadius: 'var(--r)',
            border: '1px solid var(--line-2)',
            background: 'var(--bg-inset)',
            color: 'var(--tx)',
          }}
        />
        <button
          type="button"
          onClick={handleFlag}
          disabled={busy}
          style={{
            fontFamily: 'var(--sans)',
            fontSize: 12,
            fontWeight: 600,
            padding: '7px 12px',
            borderRadius: 'var(--r)',
            border: '1px solid var(--line-2)',
            background: 'transparent',
            color: 'var(--tx)',
            cursor: busy ? 'not-allowed' : 'pointer',
            opacity: busy ? 0.5 : 1,
          }}
        >
          {busy ? t('flagging') : t('flag_btn')}
        </button>
      </div>

      {feedback && (
        <div
          className="mono"
          style={{
            fontSize: 10.5,
            marginTop: 8,
            color: feedback.ok ? 'var(--up)' : 'var(--down, var(--tx-dim))',
          }}
        >
          {feedback.msg}
        </div>
      )}
    </div>
  );
}

function EmptyState() {
  const t = useTranslations('PageAdversarial');
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
  const t = useTranslations('PageAdversarial');
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
