'use client';

import { useEffect, useState } from 'react';
import { useTranslations } from 'next-intl';
import { Panel, PanelHeader, PanelBody } from '@/components/ui/Panel';
import { LockedScreen } from '@/components/common/LockedScreen';
import { usePermissionsStore } from '@/stores/permissions';
import { ScalpingPanel } from '@/components/scalping/ScalpingPanel';
import { ChangeHistory } from '@/components/scalping/ChangeHistory';
import { BotStatusBar } from '@/components/scalping/BotStatusBar';

// Screen slug as known by the backend permissions.locks[].screen (C1.4).
const SCREEN = 'scalping';

// Scalping control surface (status bar, config panel, change history).
// Shared by the Control Panel "Scalping" tab and the /scalping page.
export function ScalpingSection() {
  const t = useTranslations('Scalping');

  // Load backend permission truth so the write gate and the lock banner read the
  // SAME source (GET /api/v1/me/permissions). Reuses the politicas pattern.
  const load = usePermissionsStore((s) => s.load);
  const isLocked = usePermissionsStore((s) => s.isLocked(SCREEN));
  const lock = usePermissionsStore((s) => s.lockFor(SCREEN));
  const loaded = usePermissionsStore((s) => s.loaded);
  useEffect(() => {
    load();
  }, [load]);

  // Write UX gate. 'scalping' is not a backend-governed lock screen, so a missing
  // lock entry means "not locked" (writable) rather than "deny" — the backend
  // still enforces level 3 on every config write, so this only governs UX.
  // Force canWrite=false until after mount so SSR and the first client paint
  // agree on every `disabled` attribute (the permissions store is a client
  // singleton whose SSR/hydration state can differ). Same fix as /xsec, /motor.
  const [mounted, setMounted] = useState(false);
  useEffect(() => {
    setMounted(true);
  }, []);
  const canWrite = mounted && loaded && (lock == null || !lock.locked);

  return (
    <div>
      {/* Lock banner — never hides content; read-only when not authorized. */}
      {isLocked && (
        <div style={{ marginBottom: 16 }}>
          <LockedScreen screen={SCREEN} bannerOnly title={t('read_only_title')} />
        </div>
      )}

      {/* Control surface */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
        <BotStatusBar canWrite={canWrite} />

        <Panel>
          <PanelHeader title={t('control_title')} sub={t('control_sub')} />
          <PanelBody>
            <ScalpingPanel canWrite={canWrite} />
          </PanelBody>
        </Panel>

        <Panel>
          <PanelHeader title={t('history_title')} sub={t('history_sub')} />
          <PanelBody>
            <ChangeHistory canWrite={canWrite} />
          </PanelBody>
        </Panel>
      </div>
    </div>
  );
}
