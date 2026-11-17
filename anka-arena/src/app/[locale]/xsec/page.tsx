'use client';

// XSec — control panel for the market-neutral cross-sectional futures basket
// strategy: current basket + exposure (StatusCard), job actions (ActionsPanel:
// sync-book / rebalance dry-run / rebalance LIVE / cancel-order), a live output
// monitor (JobMonitor), job history (JobHistory) and the strategy params table
// (ParamsEditor). Reuses the control-panel permission pattern used by Motor:
// backend permission truth via usePermissionsStore drives the write gate.

import { useEffect, useState } from 'react';
import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { Panel, PanelHeader, PanelBody } from '@/components/ui/Panel';
import { Badge } from '@/components/ui/Badge';
import { LockedScreen } from '@/components/common/LockedScreen';
import { usePermissionsStore } from '@/stores/permissions';
import { StatusCard } from '@/components/xsec/StatusCard';
import { ActiveAlertsBar } from '@/components/xsec/ActiveAlertsBar';
import { PerformanceOverview } from '@/components/xsec/PerformanceOverview';
import { ActionsPanel } from '@/components/xsec/ActionsPanel';
import { BacktestPanel } from '@/components/xsec/BacktestPanel';
import { JobMonitor } from '@/components/xsec/JobMonitor';
import { JobHistory } from '@/components/xsec/JobHistory';
import { BacktestCompareView } from '@/components/xsec/BacktestCompareView';
import { ParamsEditor } from '@/components/xsec/ParamsEditor';
import { ShadowPanel } from '@/components/xsec/ShadowPanel';
import { listXsecJobs, type XSecJob } from '@/lib/xsec-api';

const SCREEN = 'xsec';

export default function XsecPage() {
  const t = useTranslations('Xsec');

  const load = usePermissionsStore((s) => s.load);
  const isLocked = usePermissionsStore((s) => s.isLocked(SCREEN));
  const lock = usePermissionsStore((s) => s.lockFor(SCREEN));
  const loaded = usePermissionsStore((s) => s.loaded);
  useEffect(() => {
    load();
  }, [load]);

  // Guard against SSR/client hydration mismatches on every `disabled`
  // attribute derived from canWrite below: the permissions store is a client
  // module singleton, so its state at SSR time and at the client's first
  // paint (pre-hydration) aren't guaranteed to agree. Forcing canWrite=false
  // until after mount makes both renders agree (disabled) on the first
  // paint; the real permission-driven value takes over one tick later.
  const [mounted, setMounted] = useState(false);
  useEffect(() => {
    setMounted(true);
  }, []);

  const canWrite = mounted && loaded && (lock == null || !lock.locked);

  // Shared jobs poll: drives ActionsPanel's dry-run-first gating, JobMonitor's
  // "currently running" detection and JobHistory's table.
  const { data: jobs, isLoading: jobsLoading, mutate: mutateJobs } = useSWR<XSecJob[]>(
    'xsec:jobs',
    () => listXsecJobs(20),
    { revalidateOnFocus: false, refreshInterval: 5_000 },
  );
  const jobsList = jobs ?? [];
  const anyRunning = jobsList.some((j) => j.status === 'running');

  const [focusJobId, setFocusJobId] = useState<number | null>(null);
  const [conflictJobId, setConflictJobId] = useState<number | null>(null);
  const [screenMode, setScreenMode] = useState<'live' | 'backtest'>('live');
  const [compareIds, setCompareIds] = useState<Set<number>>(new Set());

  function toggleCompare(jobId: number) {
    setCompareIds((prev) => {
      const next = new Set(prev);
      if (next.has(jobId)) next.delete(jobId);
      else next.add(jobId);
      return next;
    });
  }

  function handleJobCreated(jobId: number) {
    setFocusJobId(jobId);
    setConflictJobId(null);
    void mutateJobs();
  }

  function handleConflict(runningJobId: number | null) {
    setConflictJobId(runningJobId);
  }

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

      <ActiveAlertsBar />

      <div style={{ display: 'flex', gap: 8, marginBottom: 16 }}>
        <ModeTab active={screenMode === 'live'} onClick={() => setScreenMode('live')} label={t('tab_live')} />
        <ModeTab active={screenMode === 'backtest'} onClick={() => setScreenMode('backtest')} label={t('tab_backtest')} />
      </div>

      {conflictJobId != null && (
        <div
          className="mono"
          style={{
            marginBottom: 16, fontSize: 12, padding: '10px 12px', borderRadius: 'var(--r)',
            color: 'var(--down)', background: 'rgba(233,90,90,.07)', border: '1px solid var(--down)',
          }}
        >
          {t('conflict_message')}{' '}
          <button
            type="button"
            onClick={() => setFocusJobId(conflictJobId)}
            style={{ background: 'none', border: 'none', color: 'var(--down)', textDecoration: 'underline', cursor: 'pointer', fontSize: 12, padding: 0 }}
          >
            #{conflictJobId}
          </button>
        </div>
      )}

      {screenMode === 'live' ? (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          <Panel>
            <PanelHeader title={t('performance_title')} sub={t('performance_sub')} />
            <PanelBody>
              <PerformanceOverview />
            </PanelBody>
          </Panel>

          <Panel>
            <PanelHeader title={t('status_title')} sub={t('status_sub')} />
            <PanelBody>
              <StatusCard
                canWrite={canWrite}
                anyRunning={anyRunning}
                onJobCreated={handleJobCreated}
                onConflict={handleConflict}
              />
            </PanelBody>
          </Panel>

          <Panel>
            <PanelHeader title={t('actions_title')} sub={t('actions_sub')} />
            <PanelBody>
              <ActionsPanel
                canWrite={canWrite}
                jobs={jobsList}
                anyRunning={anyRunning}
                onJobCreated={handleJobCreated}
                onConflict={handleConflict}
              />
            </PanelBody>
          </Panel>

          <Panel>
            <PanelHeader title={t('monitor_title')} sub={t('monitor_sub')} />
            <PanelBody>
              <JobMonitor jobs={jobsList} focusJobId={focusJobId} onSettled={() => void mutateJobs()} />
            </PanelBody>
          </Panel>

          <Panel>
            <PanelHeader title={t('history_title')} sub={t('history_sub')} />
            <PanelBody>
              <JobHistory jobs={jobsList} isLoading={jobsLoading} />
            </PanelBody>
          </Panel>

          <Panel>
            <PanelHeader title={t('shadow_title')} sub={t('shadow_sub')} />
            <PanelBody>
              <ShadowPanel canWrite={canWrite} />
            </PanelBody>
          </Panel>

          <Panel>
            <PanelHeader title={t('params_title')} sub={t('params_sub')} />
            <PanelBody>
              <ParamsEditor canWrite={canWrite} />
            </PanelBody>
          </Panel>
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          <Panel>
            <PanelHeader title={t('backtest_title')} sub={t('backtest_sub')} />
            <PanelBody>
              <BacktestPanel
                canWrite={canWrite}
                anyRunning={anyRunning}
                onJobCreated={handleJobCreated}
                onConflict={handleConflict}
                onAddToCompare={(id) => setCompareIds((prev) => new Set(prev).add(id))}
                jobs={jobsList}
                focusJobId={focusJobId}
                onJobsChanged={() => void mutateJobs()}
              />
            </PanelBody>
          </Panel>

          <Panel>
            <PanelHeader title={t('monitor_title')} sub={t('monitor_sub')} />
            <PanelBody>
              <JobMonitor jobs={jobsList} focusJobId={focusJobId} onSettled={() => void mutateJobs()} />
            </PanelBody>
          </Panel>

          {compareIds.size >= 2 && (
            <Panel>
              <PanelHeader title={t('compare_panel_title')} sub={t('compare_panel_sub')} />
              <PanelBody>
                <BacktestCompareView
                  jobs={jobsList.filter((j) => compareIds.has(j.id))}
                  onRemove={(id) => toggleCompare(id)}
                />
              </PanelBody>
            </Panel>
          )}

          <Panel>
            <PanelHeader title={t('backtest_history_title')} sub={t('backtest_history_sub')} />
            <PanelBody>
              <JobHistory
                jobs={jobsList.filter((j) => j.mode === 'backtest' || j.mode === 'backtest-sweep')}
                isLoading={jobsLoading}
                selectedIds={compareIds}
                onToggleSelect={toggleCompare}
                selectable={(j) => j.mode === 'backtest'}
              />
              {compareIds.size === 1 && (
                <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', marginTop: 8 }}>
                  {t('compare_need_more')}
                </div>
              )}
            </PanelBody>
          </Panel>
        </div>
      )}
    </div>
  );
}

function ModeTab({ active, onClick, label }: { active: boolean; onClick: () => void; label: string }) {
  return (
    <button
      type="button"
      onClick={onClick}
      style={{
        padding: '8px 16px', borderRadius: 'var(--r)', fontSize: 12, fontWeight: 600, cursor: 'pointer',
        border: active ? '1px solid var(--brand)' : '1px solid var(--line)',
        background: active ? 'rgba(157,124,255,.1)' : 'transparent',
        color: active ? 'var(--brand)' : 'var(--tx-dim)',
      }}
    >
      {label}
    </button>
  );
}
