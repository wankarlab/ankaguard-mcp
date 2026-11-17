'use client';

// /account/keys — exchange API keys (list without secrets, add, test, delete).
// The body is the shared CredentialsManager also used by Gremio's CopySetup.

import { useTranslations } from 'next-intl';
import { Panel, PanelHeader, PanelBody } from '@/components/ui/Panel';
import { CredentialsManager } from '@/components/account/CredentialsManager';

export default function AccountKeysPage() {
  const t = useTranslations('AccountKeys');
  return (
    <div className="content-pad" style={{ maxWidth: 900, margin: '0 auto' }}>
      <div style={{ marginBottom: 20 }}>
        <div className="mono" style={{ fontSize: 10, letterSpacing: '.08em', color: 'var(--brand)', textTransform: 'uppercase', marginBottom: 4 }}>
          {t('eyebrow')}
        </div>
        <h2 style={{ margin: 0, fontSize: 20, color: 'var(--tx-hi)', fontWeight: 700 }}>{t('title')}</h2>
      </div>
      <Panel>
        <PanelHeader title={t('panel_title')} sub={t('panel_sub')} />
        <PanelBody>
          <CredentialsManager />
        </PanelBody>
      </Panel>
    </div>
  );
}
