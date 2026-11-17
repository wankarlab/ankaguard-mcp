'use client';

import { useTranslations } from 'next-intl';
import { ApprovalsPanel } from '@/components/gate/ApprovalsPanel';

export default function AprobacionesPage() {
  return (
    <div className="content-pad enter" style={{ maxWidth: 1000, margin: '0 auto' }}>
      <PageHeader />
      <ApprovalsPanel />
    </div>
  );
}

function PageHeader() {
  const t = useTranslations('PageApprovals');
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
