'use client';

// /account/alerts — thresholds and switches of the XSEC alert monitor
// (9 xsec.alert.* params via /xsec/params). Writing needs level >= 2.

import { useEffect, useState } from 'react';
import { useTranslations } from 'next-intl';
import { Panel, PanelHeader, PanelBody } from '@/components/ui/Panel';
import { AlertPrefsForm } from '@/components/account/AlertPrefsForm';
import { usePermissionsStore } from '@/stores/permissions';

// Same level the backend requires to PUT /xsec/params.
const WRITE_LEVEL = 2;

export default function AccountAlertsPage() {
  const t = useTranslations('AccountAlerts');
  const load = usePermissionsStore((s) => s.load);
  const loaded = usePermissionsStore((s) => s.loaded);
  const userLevel = usePermissionsStore((s) => s.permissions?.userLevel ?? 0);
  useEffect(() => {
    load();
  }, [load]);

  // Render read-only until mounted so SSR and first client paint agree.
  const [mounted, setMounted] = useState(false);
  useEffect(() => {
    setMounted(true);
  }, []);

  const canWrite = mounted && loaded && userLevel >= WRITE_LEVEL;
  const readOnlyKnown = mounted && loaded && userLevel < WRITE_LEVEL;

  return (
    <div className="content-pad" style={{ maxWidth: 760, margin: '0 auto' }}>
      <div style={{ marginBottom: 20 }}>
        <div className="mono" style={{ fontSize: 10, letterSpacing: '.08em', color: 'var(--brand)', textTransform: 'uppercase', marginBottom: 4 }}>
          {t('eyebrow')}
        </div>
        <h2 style={{ margin: 0, fontSize: 20, color: 'var(--tx-hi)', fontWeight: 700 }}>{t('title')}</h2>
      </div>
      {readOnlyKnown && (
        <div className="disclaimer" style={{ marginBottom: 16 }}>
          <span>{t('read_only', { required: WRITE_LEVEL, level: userLevel })}</span>
        </div>
      )}
      <Panel>
        <PanelHeader title={t('panel_title')} sub={t('panel_sub')} />
        <PanelBody>
          <AlertPrefsForm canWrite={canWrite} />
        </PanelBody>
      </Panel>
    </div>
  );
}
