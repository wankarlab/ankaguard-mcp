'use client';

import { useTranslations } from 'next-intl';
import { Badge } from '@/components/ui/Badge';
import { ScalpingSection } from '@/components/control/ScalpingSection';

export default function ScalpingPage() {
  const t = useTranslations('Scalping');
  return (
    <div className="content-pad" style={{ maxWidth: 1100, margin: '0 auto' }}>
      {/* Header */}
      <div
        style={{
          display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between',
          flexWrap: 'wrap', gap: 10, marginBottom: 20,
        }}
      >
        <div>
          <div
            className="mono"
            style={{ fontSize: 10, letterSpacing: '.08em', color: 'var(--brand)', textTransform: 'uppercase', marginBottom: 4 }}
          >
            {t('eyebrow')}
          </div>
          <h2 style={{ margin: 0, fontSize: 20, color: 'var(--tx-hi)', fontWeight: 700 }}>
            {t('title')}
          </h2>
        </div>
        <Badge variant="brand">{t('badge')}</Badge>
      </div>

      <ScalpingSection />
    </div>
  );
}
