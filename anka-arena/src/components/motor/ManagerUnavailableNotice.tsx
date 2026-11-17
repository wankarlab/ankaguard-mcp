'use client';

// Shown instead of the bot table / master paper-mode switch when the backend
// answers 503 (multi-pair bot manager not initialized).

import { useTranslations } from 'next-intl';

export function ManagerUnavailableNotice({ compact = false }: { compact?: boolean }) {
  const t = useTranslations('Motor');
  return (
    <div
      role="status"
      style={{
        padding: compact ? '8px 12px' : 14, borderRadius: 'var(--r)',
        border: '1px solid var(--line)', background: 'var(--bg-inset)',
      }}
    >
      <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--tx-hi)' }}>{t('manager_unavailable_title')}</div>
      <div style={{ fontSize: 11, color: 'var(--tx-dim)', marginTop: 4 }}>{t('manager_unavailable_body')}</div>
    </div>
  );
}
