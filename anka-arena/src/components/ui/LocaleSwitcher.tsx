'use client';

import { useLocale, useTranslations } from 'next-intl';
import { useRouter, usePathname } from '@/i18n/routing';
import { useTransition } from 'react';

export function LocaleSwitcher() {
  const t = useTranslations('LocaleSwitcher');
  const locale = useLocale();
  const router = useRouter();
  const pathname = usePathname();
  const [isPending, startTransition] = useTransition();

  function onSelectChange(nextLocale: string) {
    startTransition(() => {
      router.replace(pathname, { locale: nextLocale });
    });
  }

  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginLeft: 16 }}>
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="var(--tx-dim)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <circle cx="12" cy="12" r="10" />
        <line x1="2" y1="12" x2="22" y2="12" />
        <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z" />
      </svg>
      <select
        defaultValue={locale}
        disabled={isPending}
        onChange={(e) => onSelectChange(e.target.value)}
        aria-label={t('label')}
        style={{
          background: 'transparent',
          border: 'none',
          color: 'var(--tx-hi)',
          fontFamily: 'var(--mono)',
          fontSize: 11,
          cursor: isPending ? 'wait' : 'pointer',
          outline: 'none',
          appearance: 'none',
          paddingRight: 10
        }}
      >
        <option value="es">{t('es')}</option>
        <option value="en">{t('en')}</option>
      </select>
    </div>
  );
}
