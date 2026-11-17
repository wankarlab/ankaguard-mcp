'use client';

// System health summary. There is no dedicated health detail screen, so the
// per-component detail expands inline.

import { useState } from 'react';
import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { getSystemHealth, type SystemHealth, type HealthState } from '@/lib/home-api';
import { affectedComponents } from '@/lib/home-logic';
import { HomeBlock, BlockMessage } from './HomeBlock';

const STATE_COLOR: Record<HealthState, string> = {
  ok: 'var(--up)',
  degraded: 'var(--warn)',
  down: 'var(--down)',
};

export function HomeHealthBlock() {
  const t = useTranslations('Home');
  const [open, setOpen] = useState(false);
  const { data, error, isLoading } = useSWR<SystemHealth>('home:system-health', getSystemHealth, {
    revalidateOnFocus: false,
    refreshInterval: 30_000,
  });

  const components = data?.components ?? [];
  const affected = affectedComponents(components);
  const state: HealthState | null = data && (data.status === 'ok' || data.status === 'degraded' || data.status === 'down') ? data.status : null;

  return (
    <HomeBlock title={t('health_title')}>
      {isLoading && !data && <BlockMessage text={t('loading')} />}
      {error && !data && <BlockMessage text={t('unavailable')} tone="error" />}
      {data && !state && <BlockMessage text={t('no_data')} />}
      {data && state && (
        <>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
            <span className="mono" style={{ fontSize: 16, fontWeight: 700, color: STATE_COLOR[state] }}>
              {t(`health_state_${state}`)}
            </span>
            {affected.length > 0 && (
              <span className="mono" style={{ fontSize: 11, color: 'var(--tx-dim)' }}>
                {t('health_affected', { names: affected.map((c) => c.name).join(', ') })}
              </span>
            )}
          </div>
          {components.length > 0 && (
            <button
              type="button"
              aria-expanded={open}
              onClick={() => setOpen((v) => !v)}
              className="mono"
              style={{ marginTop: 8, background: 'none', border: 'none', padding: 0, cursor: 'pointer', fontSize: 11, color: 'var(--brand)' }}
            >
              {open ? t('health_hide') : t('health_show')}
            </button>
          )}
          {open && (
            <div style={{ marginTop: 8, display: 'flex', flexDirection: 'column', gap: 4 }}>
              {components.map((c) => (
                <div key={c.name} className="mono" style={{ display: 'flex', gap: 10, fontSize: 11, flexWrap: 'wrap' }}>
                  <span style={{ minWidth: 130, color: 'var(--tx-hi)' }}>{c.name}</span>
                  <span style={{ color: c.status === 'ok' ? 'var(--up)' : 'var(--down)' }}>{c.status}</span>
                  <span style={{ color: 'var(--tx-faint)' }}>{c.latency_ms} ms</span>
                  <span style={{ color: 'var(--tx-dim)' }}>{c.message || '—'}</span>
                </div>
              ))}
            </div>
          )}
        </>
      )}
    </HomeBlock>
  );
}
