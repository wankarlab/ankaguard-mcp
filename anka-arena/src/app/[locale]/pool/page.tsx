'use client';

// C3.5 — Pool (LP): the legal-gated locked door, nothing more.
//
// This screen is intentionally NOT a feature surface. The pool (liquidity
// provider / NAV) requires a higher level + KYC that today's user does not
// have. Per rule #4 (never hide a locked screen), we render the door VISIBLE
// with the LockedScreen banner whose state (locked/required level/how to
// unlock) comes from GET /api/v1/me/permissions — no invented NAV, no mock
// numbers, no fake pool stats.

import { useTranslations } from 'next-intl';
import { LockedScreen } from '@/components/common/LockedScreen';

export default function PoolPage() {
  const t = useTranslations('PagePool');
  return (
    <div className="content-pad enter" style={{ maxWidth: 760, margin: '0 auto' }}>
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
        <p style={{ margin: '6px 0 0', fontSize: 12.5, color: 'var(--tx-dim)', maxWidth: 600 }}>
          {t('intro')}
        </p>
      </div>

      <LockedScreen screen="pool" title={t('locked_title')} />
    </div>
  );
}
