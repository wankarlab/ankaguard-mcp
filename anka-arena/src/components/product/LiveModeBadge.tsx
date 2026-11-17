'use client';

// Permanent real-money indicator (product edition). The state comes only from
// GET /api/v1/product/status.live_enabled; any failure (including a backend
// that does not serve the endpoint yet) shows "unknown mode", never a guess.

import { useTranslations } from 'next-intl';
import { liveModeOf, type LiveMode } from '@/lib/product-api';
import { useProductStatus } from './hooks';

const STYLE: Record<LiveMode, { color: string; bg: string; border: string }> = {
  live: { color: 'var(--real, #f0564f)', bg: 'var(--real-soft, rgba(240,86,79,.12))', border: 'rgba(240,86,79,.45)' },
  off: { color: 'var(--ok, #36cd86)', bg: 'var(--up-soft, rgba(54,205,134,.1))', border: 'rgba(54,205,134,.35)' },
  unknown: { color: 'var(--warn, #e9a23e)', bg: 'rgba(233,162,62,.1)', border: 'rgba(233,162,62,.4)' },
};

export function LiveModeIndicator({ mode }: { mode: LiveMode }) {
  const t = useTranslations('Product');
  const s = STYLE[mode];
  return (
    <span
      role="status"
      data-live-mode={mode}
      className="mono"
      title={t(`mode_${mode}_hint`)}
      style={{
        display: 'inline-flex', alignItems: 'center', gap: 6, padding: '3px 10px', borderRadius: 999,
        fontSize: 11, fontWeight: 700, letterSpacing: '.04em', whiteSpace: 'nowrap',
        color: s.color, background: s.bg, border: `1px solid ${s.border}`,
      }}
    >
      <span aria-hidden style={{ width: 7, height: 7, borderRadius: '50%', background: s.color }} />
      {t(`mode_${mode}`)}
    </span>
  );
}

export function LiveModeBadge() {
  const { data, error } = useProductStatus();
  // While loading there is no data yet: report unknown rather than assume.
  return <LiveModeIndicator mode={error ? 'unknown' : liveModeOf(data)} />;
}
