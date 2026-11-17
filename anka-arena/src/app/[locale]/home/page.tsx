'use client';

// /home — operator landing: real equity, XSEC basket + alerts, system health,
// kill switch and logbook. Every block fetches its own data and renders its
// own empty/error state, so one failing endpoint never blanks the screen.

import { useTranslations } from 'next-intl';
import { HomeEquityBlock } from '@/components/home/HomeEquityBlock';
import { HomeBasketBlock } from '@/components/home/HomeBasketBlock';
import { HomeHealthBlock } from '@/components/home/HomeHealthBlock';
import { HomeKillSwitchBlock } from '@/components/home/HomeKillSwitchBlock';
import { HomeLogbookBlock } from '@/components/home/HomeLogbookBlock';
import { HomeLicenseNotice } from '@/components/product/LicenseParts';
import { isProductEdition } from '@/lib/productMode';

export default function HomePage() {
  const t = useTranslations('Home');
  return (
    <div className="content-pad" style={{ maxWidth: 1100, margin: '0 auto' }}>
      <div style={{ marginBottom: 20 }}>
        <div className="mono" style={{ fontSize: 10, letterSpacing: '.08em', color: 'var(--brand)', textTransform: 'uppercase', marginBottom: 4 }}>
          {t('eyebrow')}
        </div>
        <h2 style={{ margin: 0, fontSize: 20, color: 'var(--tx-hi)', fontWeight: 700 }}>{t('title')}</h2>
      </div>
      {isProductEdition() && (
        <div style={{ marginBottom: 16 }}>
          <HomeLicenseNotice />
        </div>
      )}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 420px), 1fr))', gap: 16, alignItems: 'start' }}>
        <HomeEquityBlock />
        {/* The XSEC basket is not a product screen (its link would be "not found"). */}
        {!isProductEdition() && <HomeBasketBlock />}
        <HomeHealthBlock />
        <HomeKillSwitchBlock />
        <div style={{ gridColumn: '1 / -1' }}>
          <HomeLogbookBlock />
        </div>
      </div>
    </div>
  );
}
