'use client';

// Control Panel — full dynamic-config control surface (Phases 3-5):
//   - Parámetros : ParameterBrowser over the 35 registry groups (live edit).
//   - Modos      : ModeManager (CRUD + apply/rollback over category="mode" presets).
//   - Protección : ManualProtection (adopt toggle + positions table).
//
// Reuses the scalping page's permission pattern: backend permission truth via
// usePermissionsStore drives the write gate and the read-only banner. The
// backend enforces level 3+ on every config write regardless of UI state.

import { use, useEffect, useState } from 'react';
import { useTranslations } from 'next-intl';
import { Panel, PanelHeader, PanelBody } from '@/components/ui/Panel';
import { Badge } from '@/components/ui/Badge';
import { LockedScreen } from '@/components/common/LockedScreen';
import { usePermissionsStore } from '@/stores/permissions';
import { ParameterBrowser } from '@/components/control/ParameterBrowser';
import { ModeManager } from '@/components/control/ModeManager';
import { ManualProtection } from '@/components/control/ManualProtection';
import { ScalpingSection } from '@/components/control/ScalpingSection';
import { SizingSection } from '@/components/control/SizingSection';
import { isProductEdition } from '@/lib/productMode';

const SCREEN = 'control-panel';

type Tab = 'sizing' | 'params' | 'modes' | 'protection' | 'scalping';
// Product edition exposes only "sizing and caps"; other editions keep every tab.
const PRODUCT = isProductEdition();
const TABS: Tab[] = PRODUCT ? ['sizing'] : ['sizing', 'params', 'modes', 'protection', 'scalping'];

export default function ControlPanelPage({
  searchParams,
}: {
  searchParams: Promise<{ tab?: string | string[] }>;
}) {
  const { tab: tabParam } = use(searchParams);
  const initialTab = (TABS as string[]).includes(String(tabParam)) ? (tabParam as Tab) : PRODUCT ? 'sizing' : 'params';
  const t = useTranslations('Control');

  const load = usePermissionsStore((s) => s.load);
  const isLocked = usePermissionsStore((s) => s.isLocked(SCREEN));
  const lock = usePermissionsStore((s) => s.lockFor(SCREEN));
  const loaded = usePermissionsStore((s) => s.loaded);
  useEffect(() => {
    load();
  }, [load]);

  // Like scalping: a missing lock entry means "not a governed lock screen" →
  // writable in the UI; backend still enforces level 3 on writes.
  // Force canWrite=false until after mount so SSR and the first client paint
  // agree on every `disabled` attribute (the permissions store is a client
  // singleton whose SSR/hydration state can differ). Same fix as /xsec, /motor.
  const [mounted, setMounted] = useState(false);
  useEffect(() => {
    setMounted(true);
  }, []);
  const canWrite = mounted && loaded && (lock == null || !lock.locked);

  const [tab, setTab] = useState<Tab>(initialTab);

  const allTabs: { id: Tab; labelKey: string }[] = [
    { id: 'sizing', labelKey: 'tab_sizing' },
    { id: 'params', labelKey: 'tab_params' },
    { id: 'modes', labelKey: 'tab_modes' },
    { id: 'protection', labelKey: 'tab_protection' },
    { id: 'scalping', labelKey: 'tab_scalping' },
  ];
  const tabs = allTabs.filter((tb) => TABS.includes(tb.id));

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

      {/* Tabs */}
      <div style={{ display: 'flex', gap: 6, marginBottom: 16, flexWrap: 'wrap' }}>
        {tabs.map((tb) => (
          <button
            key={tb.id}
            type="button"
            onClick={() => setTab(tb.id)}
            style={{
              padding: '8px 16px', borderRadius: 'var(--r)', fontSize: 13, cursor: 'pointer',
              border: `1px solid ${tab === tb.id ? 'var(--brand)' : 'var(--line)'}`,
              background: tab === tb.id ? 'rgba(70,200,216,.08)' : 'transparent',
              color: tab === tb.id ? 'var(--brand)' : 'var(--tx-dim)',
              fontWeight: tab === tb.id ? 600 : 400,
            }}
          >
            {t(tb.labelKey)}
          </button>
        ))}
      </div>

      {/* Content */}
      {tab === 'params' && (
        <Panel>
          <PanelHeader title={t('params_title')} sub={t('params_sub')} />
          <PanelBody>
            <ParameterBrowser mode="edit" canWrite={canWrite} />
          </PanelBody>
        </Panel>
      )}

      {tab === 'modes' && (
        <Panel>
          <PanelHeader title={t('modes_title')} sub={t('modes_sub')} />
          <PanelBody>
            <ModeManager canWrite={canWrite} />
          </PanelBody>
        </Panel>
      )}

      {tab === 'protection' && (
        <Panel>
          <PanelHeader title={t('protection_title')} sub={t('protection_sub')} />
          <PanelBody>
            <ManualProtection canWrite={canWrite} />
          </PanelBody>
        </Panel>
      )}

      {tab === 'scalping' && <ScalpingSection />}

      {tab === 'sizing' && <SizingSection canWrite={canWrite} />}
    </div>
  );
}
