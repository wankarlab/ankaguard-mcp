'use client';

// Motor — operational-control surface for the master agent + bot fleet:
//   - MasterControls : start/stop/tick, global paper-mode, emergency-stop-all.
//   - BotTable       : per-bot kill + paper-mode toggle, opportunities, xsec.
//
// Reuses the control-panel permission pattern: backend permission truth via
// usePermissionsStore drives the write gate and the read-only banner. On the
// backend, every master/bot control write requires a valid JWT (the control
// routes are JWT-gated on the REST port), and emergency-stop-all additionally
// requires level 4 — enforced server-side regardless of UI state.

import { useEffect, useState } from 'react';
import { useTranslations } from 'next-intl';
import { Panel, PanelHeader, PanelBody } from '@/components/ui/Panel';
import { Badge } from '@/components/ui/Badge';
import { LockedScreen } from '@/components/common/LockedScreen';
import { usePermissionsStore } from '@/stores/permissions';
import { MasterControls } from '@/components/motor/MasterControls';
import { BotTable } from '@/components/motor/BotTable';

const SCREEN = 'motor';

export default function MotorPage() {
  const t = useTranslations('Motor');

  const load = usePermissionsStore((s) => s.load);
  const isLocked = usePermissionsStore((s) => s.isLocked(SCREEN));
  const lock = usePermissionsStore((s) => s.lockFor(SCREEN));
  const loaded = usePermissionsStore((s) => s.loaded);
  useEffect(() => {
    load();
  }, [load]);

  // Guard against SSR/client hydration mismatches on every `disabled` attribute
  // derived from canWrite: the permissions store is a client singleton, so its
  // state at SSR time and at the client's first paint (pre-hydration) aren't
  // guaranteed to agree. Forcing canWrite=false until after mount makes both
  // renders agree (disabled) on the first paint; the real permission-driven
  // value takes over one tick later. Same fix as the /xsec page.
  const [mounted, setMounted] = useState(false);
  useEffect(() => {
    setMounted(true);
  }, []);

  // A missing lock entry means "not a governed lock screen" → writable in the UI;
  // backend still enforces JWT auth (and level 4 for emergency-stop-all) on writes.
  const canWrite = mounted && loaded && (lock == null || !lock.locked);

  return (
    <div className="content-pad" style={{ maxWidth: 1100, margin: '0 auto' }}>
      {/* Header */}
      <div
        style={{
          display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between',
          flexWrap: 'wrap', gap: 10, marginBottom: 20,
        }}
      >
        <div>
          <div
            className="mono"
            style={{ fontSize: 10, letterSpacing: '.08em', color: 'var(--brand)', textTransform: 'uppercase', marginBottom: 4 }}
          >
            {t('eyebrow')}
          </div>
          <h2 style={{ margin: 0, fontSize: 20, color: 'var(--tx-hi)', fontWeight: 700 }}>{t('title')}</h2>
        </div>
        <Badge variant="brand">{t('badge')}</Badge>
      </div>

      {isLocked && (
        <div style={{ marginBottom: 16 }}>
          <LockedScreen screen={SCREEN} bannerOnly title={t('read_only_title')} />
        </div>
      )}

      <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
        <Panel>
          <PanelHeader title={t('master_title')} sub={t('master_sub')} />
          <PanelBody>
            <MasterControls canWrite={canWrite} />
          </PanelBody>
        </Panel>

        <Panel>
          <PanelHeader title={t('fleet_title')} sub={t('fleet_sub')} />
          <PanelBody>
            <BotTable canWrite={canWrite} />
          </PanelBody>
        </Panel>
      </div>
    </div>
  );
}
