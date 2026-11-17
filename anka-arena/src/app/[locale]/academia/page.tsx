'use client';

import { useTranslations } from 'next-intl';
import { ReplayFeed } from '@/components/academia/ReplayFeed';
import { CourseProgress } from '@/components/academia/CourseProgress';
import { CohortStats } from '@/components/academia/CohortStats';

// Note: metadata export only works in server components.
// Since this page is 'use client' (needed for store access via children),
// set the title via the layout metadata instead.

export default function AcademiaPage() {
  const t = useTranslations('PageAcademy');
  return (
    <div className="content-pad">
      <div className="maxw">
        {/* Permanent disclaimer — VISION §12.6 */}
        <div style={{
          display:     'flex',
          alignItems:  'flex-start',
          gap:         10,
          padding:     '10px 14px',
          marginBottom: 18,
          background:  'var(--bg-panel)',
          border:      '1px solid var(--line-2)',
          borderRadius: 'var(--r)',
          fontSize:    12,
          color:       'var(--tx-dim)',
        }}>
          <span style={{ color: 'var(--tx-faint)', fontSize: 13, marginTop: 0, flexShrink: 0 }}>ℹ</span>
          <span>
            <b style={{ color: 'var(--tx)' }}>{t('disclaimer_title')}</b>
            {' '}{t('disclaimer_body')}
          </span>
        </div>

        {/* Main grid: feed (left) + sidebar (right) */}
        <div style={{
          display:             'grid',
          gridTemplateColumns: 'minmax(0, 1fr) 300px',
          gap:                 16,
          alignItems:          'start',
        }}>
          {/* Left: replay feed */}
          <ReplayFeed />

          {/* Right: sidebar */}
          <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
            <CourseProgress />
            <CohortStats />
          </div>
        </div>
      </div>
    </div>
  );
}
