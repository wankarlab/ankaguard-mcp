'use client';

import { useEffect } from 'react';
import { useTranslations } from 'next-intl';
import { Panel, PanelHeader, PanelBody } from '@/components/ui/Panel';
import { PolicySliders } from '@/components/politicas/PolicySliders';
import { RobustnessWeights } from '@/components/politicas/RobustnessWeights';
import { Capa0Panel } from '@/components/politicas/Capa0Panel';
import { TierTable } from '@/components/politicas/TierTable';
import { MoonshotList } from '@/components/politicas/MoonshotList';
import { Badge } from '@/components/ui/Badge';
import { LockedScreen } from '@/components/common/LockedScreen';
import { usePermissionsStore } from '@/stores/permissions';

// Screen slug as known by the backend permissions.locks[].screen (C1.4).
const SCREEN = 'politicas';

export default function PoliticasPage() {
  const t = useTranslations('Politicas');
  // Ensure permissions are loaded so the write gate inside PolicySliders and the
  // LockedScreen banner read the SAME backend truth (GET /api/v1/me/permissions).
  const load = usePermissionsStore((s) => s.load);
  const isLocked = usePermissionsStore((s) => s.isLocked(SCREEN));
  useEffect(() => {
    load();
  }, [load]);

  return (
    <div className="content-pad" style={{ maxWidth: 1100, margin: '0 auto' }}>
      {/* Page header */}
      <div
        style={{
          display: 'flex',
          alignItems: 'flex-start',
          justifyContent: 'space-between',
          flexWrap: 'wrap',
          gap: 10,
          marginBottom: 20,
        }}
      >
        <div>
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
        </div>
        <Badge variant="brand">
          {t('badge')}
        </Badge>
      </div>

      {/* C1.4 — Lock banner. NEVER hides content: when the caller lacks the
          backend-required level the page stays in read-only mode and this
          banner (LockedScreen bannerOnly) explains the required level + how to
          unlock. The write ceremony inside PolicySliders enforces the same
          backend truth. When unlocked, no banner is shown. */}
      {isLocked && (
        <div style={{ marginBottom: 16 }}>
          <LockedScreen
            screen={SCREEN}
            bannerOnly
            title={t('read_only_title')}
          />
        </div>
      )}

      {/* Row 1: Gate thresholds + Robustness weights + Capa 0 */}
      <div
        style={{
          display: 'grid',
          gridTemplateColumns: 'minmax(0,1fr) minmax(0,1fr)',
          gap: 16,
          marginBottom: 16,
          alignItems: 'start',
        }}
      >
        {/* Gate thresholds */}
        <Panel>
          <PanelHeader
            title={t('gate_thresholds')}
            sub={t('gate_thresholds_sub')}
          />
          <PolicySliders />
        </Panel>

        {/* Right column: Robustness weights + Capa 0 */}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          <Panel>
            <PanelHeader
              title={t('robustness')}
              sub={t('robustness_sub')}
            />
            <RobustnessWeights />
          </Panel>

          <Panel>
            <PanelHeader
              title={t('capa0')}
              sub={t('capa0_sub')}
            />
            <Capa0Panel />
          </Panel>
        </div>
      </div>

      {/* Row 2: Tiers + Moonshots */}
      <div
        style={{
          display: 'grid',
          gridTemplateColumns: 'minmax(0,1.5fr) minmax(0,1fr)',
          gap: 16,
          alignItems: 'start',
        }}
      >
        <Panel>
          <PanelHeader
            title={t('tiers')}
            sub={t('tiers_sub')}
          />
          <PanelBody>
            <TierTable />
          </PanelBody>
        </Panel>

        <Panel>
          <PanelHeader
            title={t('moonshots')}
            sub={t('moonshots_sub')}
          />
          <MoonshotList />
        </Panel>
      </div>
    </div>
  );
}
