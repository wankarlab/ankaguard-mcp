'use client';

// Launch — AnkaLaunch phase 1 (shadow) console: summary with the edge verdict,
// opportunities, manual approval queue, shadow positions and reports. Everything
// is simulation; the banner is permanent on every tab.

import { useState } from 'react';
import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { Badge } from '@/components/ui/Badge';
import { getLaunchStatus, isDegraded, launchErrorInfo } from '@/lib/launch-api';
import { fmtTime } from '@/components/launch/shared';
import { LaunchSummary } from '@/components/launch/LaunchSummary';
import { OpportunitiesTable } from '@/components/launch/OpportunitiesTable';
import { ApprovalQueue } from '@/components/launch/ApprovalQueue';
import { ShadowPositions } from '@/components/launch/ShadowPositions';
import { HumanLiftReport } from '@/components/launch/HumanLiftReport';

type Tab = 'summary' | 'opportunities' | 'approval' | 'positions' | 'reports';
const TABS: Tab[] = ['summary', 'opportunities', 'approval', 'positions', 'reports'];

export default function LaunchPage() {
  const t = useTranslations('Launch');
  const [tab, setTab] = useState<Tab>('summary');
  const { data: status, error, isLoading, mutate } = useSWR('launch:status', getLaunchStatus, {
    revalidateOnFocus: false,
    refreshInterval: 30_000,
  });
  const unavailable = launchErrorInfo(error).status === 503;

  return (
    <div className="content-pad" style={{ maxWidth: 1100, margin: '0 auto' }}>
      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', flexWrap: 'wrap', gap: 10, marginBottom: 16 }}>
        <div>
          <div className="mono" style={{ fontSize: 10, letterSpacing: '.08em', color: 'var(--brand)', textTransform: 'uppercase', marginBottom: 4 }}>
            {t('eyebrow')}
          </div>
          <h2 style={{ margin: 0, fontSize: 20, color: 'var(--tx-hi)', fontWeight: 700 }}>{t('title')}</h2>
        </div>
        <Badge variant="brand">{t('badge')}</Badge>
      </div>

      <div
        role="status"
        style={{ padding: '10px 14px', marginBottom: 12, borderRadius: 'var(--r)', border: '1px solid var(--warn)', background: 'rgba(233,162,62,.08)', color: 'var(--warn)', fontSize: 13, fontWeight: 600 }}
      >
        {t('sim_banner')}
      </div>

      {isDegraded(status) && status && (
        <div role="alert" style={{ padding: '10px 14px', marginBottom: 12, borderRadius: 'var(--r)', border: '1px solid var(--down)', background: 'rgba(233,90,90,.06)', fontSize: 12 }}>
          <div style={{ color: 'var(--down)', fontWeight: 600 }}>{t('degraded_title')}</div>
          <div style={{ color: 'var(--tx-dim)', marginTop: 2 }}>
            {status.service === 'never_started' ? t('degraded_never') : t('degraded_down', { time: fmtTime(status.last_heartbeat) })}
          </div>
        </div>
      )}

      <div style={{ display: 'flex', gap: 8, marginBottom: 16, flexWrap: 'wrap' }}>
        {TABS.map((k) => (
          <button
            key={k}
            type="button"
            onClick={() => setTab(k)}
            style={{
              padding: '8px 16px', borderRadius: 'var(--r)', fontSize: 12, fontWeight: 600, cursor: 'pointer',
              border: tab === k ? '1px solid var(--brand)' : '1px solid var(--line)',
              background: tab === k ? 'rgba(157,124,255,.1)' : 'transparent',
              color: tab === k ? 'var(--brand)' : 'var(--tx-dim)',
            }}
          >
            {t(`tab_${k}` as never)}
          </button>
        ))}
      </div>

      {tab === 'summary' && (
        unavailable ? <div style={{ fontSize: 12, color: 'var(--down)' }}>{t('unavailable')}</div>
        : error ? <div style={{ fontSize: 12, color: 'var(--down)' }}>{t('load_error')}</div>
        : isLoading || !status ? <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{t('loading')}</div>
        : <LaunchSummary status={status} onChanged={() => void mutate()} />
      )}
      {tab === 'opportunities' && <OpportunitiesTable />}
      {tab === 'approval' && <ApprovalQueue />}
      {tab === 'positions' && <ShadowPositions />}
      {tab === 'reports' && <HumanLiftReport />}
    </div>
  );
}
