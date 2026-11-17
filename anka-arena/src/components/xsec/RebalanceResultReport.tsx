'use client';

// RebalanceResultReport — structured "why did the live deploy enter partial?"
// view over a rebalance-live/relaunch job's execution output. Surfaces the
// exact reconciliation the operator would otherwise have to read from the raw
// log: how many legs filled, which ones failed and WHY (almost always a pair
// whose minimum contract notional exceeds the per-leg target), and the
// resulting net exposure. When missing legs remain and an onComplete handler
// is provided (ActionsPanel), it offers a one-click "resize and complete" that
// re-deploys the missing legs at a per-leg size big enough to fit them.

import { useTranslations } from 'next-intl';
import type { RebalanceResult } from '@/lib/xsec-api';

interface Props {
  result: RebalanceResult;
  // When provided, shows the "resize and complete" action (ActionsPanel only).
  onComplete?: () => void;
  completing?: boolean;
  completeDisabled?: boolean;
}

function usd(n: number): string {
  if (!isFinite(n)) return '—';
  return `$${n.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

export function RebalanceResultReport({ result, onComplete, completing, completeDisabled }: Props) {
  const t = useTranslations('Xsec');
  const filled = result.okCount + result.driftCount;
  const total = filled + result.missingCount;
  const oversizeFails = result.failedLegs.filter((l) => l.contractUsd > 0);

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
      {/* Summary row */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(110px, 1fr))', gap: 8 }}>
        <Metric label={t('rr_filled')} value={`${filled}/${total}`} tone={result.missingCount === 0 ? 'up' : 'warn'} />
        <Metric label={t('rr_missing')} value={String(result.missingCount)} tone={result.missingCount === 0 ? 'neutral' : 'down'} />
        <Metric label={t('rr_imbalance')} value={`${result.imbalancePct.toFixed(1)}%`} tone={result.netWarning ? 'down' : 'up'} />
        <Metric label={t('rr_net')} value={`${usd(result.netLongUsd)} / ${usd(result.netShortUsd)}`} tone="neutral" />
      </div>

      {result.missingCount === 0 ? (
        <div className="mono" style={{ fontSize: 11, color: 'var(--up)' }}>{t('rr_all_in')}</div>
      ) : (
        <>
          <div className="mono" style={{ fontSize: 11, color: 'var(--tx-dim)' }}>
            {t('rr_why_intro', { n: result.missingCount })}
          </div>
          {oversizeFails.length > 0 && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
              {oversizeFails
                .slice()
                .sort((a, b) => b.contractUsd - a.contractUsd)
                .map((l) => (
                  <div key={l.pair} className="mono" style={{ display: 'flex', gap: 8, alignItems: 'baseline', fontSize: 11 }}>
                    <span style={{ color: 'var(--down)' }}>🚫</span>
                    <span style={{ color: 'var(--tx-hi)', width: 96, flexShrink: 0 }}>{l.pair}</span>
                    <span style={{ color: 'var(--tx-dim)' }}>
                      {t('rr_leg_reason', { contract: usd(l.contractUsd), ratio: l.ratio.toFixed(1), target: usd(l.targetUsd) })}
                    </span>
                  </div>
                ))}
            </div>
          )}
          {result.failedLegs.some((l) => l.contractUsd === 0) && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
              {result.failedLegs.filter((l) => l.contractUsd === 0).map((l) => (
                <div key={l.pair} className="mono" style={{ display: 'flex', gap: 8, alignItems: 'baseline', fontSize: 11 }}>
                  <span style={{ color: 'var(--down)' }}>❌</span>
                  <span style={{ color: 'var(--tx-hi)', width: 96, flexShrink: 0 }}>{l.pair}</span>
                  <span style={{ color: 'var(--tx-dim)' }}>{l.reason ?? t('rr_leg_generic')}</span>
                </div>
              ))}
            </div>
          )}

          {isFinite(result.minPerLegToFitUsd) && (
            <div className="mono" style={{ fontSize: 11, color: 'var(--warn)' }}>
              {t('rr_fit_hint', { min: usd(result.minPerLegToFitUsd) })}
            </div>
          )}

          {onComplete && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
              <button
                type="button"
                disabled={completing || completeDisabled}
                suppressHydrationWarning
                onClick={onComplete}
                style={{
                  alignSelf: 'flex-start',
                  padding: '9px 18px', borderRadius: 'var(--r)', border: 'none',
                  background: (completing || completeDisabled) ? 'var(--bg-void)' : 'var(--brand)',
                  color: (completing || completeDisabled) ? 'var(--tx-faint)' : 'var(--bg)',
                  fontSize: 13, fontWeight: 600,
                  cursor: (completing || completeDisabled) ? 'not-allowed' : 'pointer', whiteSpace: 'nowrap',
                }}
              >
                {completing ? t('working') : t('rr_complete_btn')}
              </button>
              <span className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)' }}>{t('rr_complete_hint')}</span>
            </div>
          )}
        </>
      )}
    </div>
  );
}

function Metric({ label, value, tone }: { label: string; value: string; tone: 'up' | 'down' | 'warn' | 'neutral' }) {
  const color = tone === 'up' ? 'var(--up)' : tone === 'down' ? 'var(--down)' : tone === 'warn' ? 'var(--warn)' : 'var(--tx-hi)';
  return (
    <div style={{ padding: '8px 10px', borderRadius: 'var(--r)', border: '1px solid var(--line)', background: 'var(--bg-inset)' }}>
      <div className="mono" style={{ fontSize: 9, color: 'var(--tx-faint)', textTransform: 'uppercase', marginBottom: 3 }}>{label}</div>
      <div className="mono" style={{ fontSize: 14, fontWeight: 700, color }}>{value}</div>
    </div>
  );
}
