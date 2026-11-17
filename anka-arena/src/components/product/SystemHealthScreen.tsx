'use client';

// /system-health — aggregated health with the per-component detail always
// open, refreshed every 15 s. Same endpoint as the Home summary block.

import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { Panel, PanelHeader, PanelBody } from '@/components/ui/Panel';
import { getSystemHealth, type HealthState, type SystemHealth } from '@/lib/home-api';
import { describeApiError } from '@/lib/product-api';
import { Notice, PageHeader } from './shared';

const STATE_COLOR: Record<HealthState, string> = { ok: 'var(--up)', degraded: 'var(--warn)', down: 'var(--down)' };

export function SystemHealthView({ health }: { health: SystemHealth }) {
  const t = useTranslations('Product');
  const state = health.status;
  const known = state === 'ok' || state === 'degraded' || state === 'down';
  const components = health.components ?? [];
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
      <div className="mono" style={{ fontSize: 18, fontWeight: 700, color: known ? STATE_COLOR[state] : 'var(--tx-dim)' }}>
        {known ? t(`health_state_${state}`) : t('health_no_data')}
      </div>
      {health.checked_at && (
        <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{t('health_checked', { at: health.checked_at })}</div>
      )}
      {components.length === 0 && known && <div className="mono" style={{ fontSize: 12, color: 'var(--tx-faint)' }}>{t('health_no_components')}</div>}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
        {components.map((c) => (
          <div key={c.name} className="mono" style={{ display: 'flex', gap: 12, fontSize: 12, flexWrap: 'wrap', padding: '6px 0', borderTop: '1px solid var(--line)' }}>
            <span style={{ minWidth: 160, color: 'var(--tx-hi)', fontWeight: 600 }}>{c.name}</span>
            <span style={{ color: c.status === 'ok' ? 'var(--up)' : 'var(--down)' }}>{c.status}</span>
            {c.critical && <span style={{ color: 'var(--tx-faint)' }}>{t('health_critical')}</span>}
            <span style={{ color: 'var(--tx-faint)' }}>{c.latency_ms} ms</span>
            <span style={{ color: 'var(--tx-dim)', overflowWrap: 'anywhere' }}>{c.message || '—'}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

export function SystemHealthScreen() {
  const t = useTranslations('Product');
  const { data, error, isLoading } = useSWR<SystemHealth>('product:system-health', getSystemHealth, {
    revalidateOnFocus: false,
    refreshInterval: 15_000,
  });
  return (
    <div className="content-pad" style={{ maxWidth: 900, margin: '0 auto' }}>
      <PageHeader eyebrow={t('health_eyebrow')} title={t('health_title')} />
      <Panel>
        <PanelHeader title={t('health_panel')} sub={t('health_sub')} />
        <PanelBody>
          {isLoading && !data && <div className="mono" style={{ fontSize: 12, color: 'var(--tx-faint)' }}>{t('loading')}</div>}
          {error && !data && <Notice tone="error">{describeApiError(error, t('unavailable'))}</Notice>}
          {data && <SystemHealthView health={data} />}
        </PanelBody>
      </Panel>
    </div>
  );
}
