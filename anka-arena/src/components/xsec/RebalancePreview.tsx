'use client';

// RebalancePreview — structured "how will the basket actually end up" view of
// a rebalance-dry job's real output (real pairs, real prices, from the
// dry-run's own live exchange/price reads — nothing simulated client-side).
// Parses the `--- DIFF ---` block via parseRebalancePlan; if parsing fails
// for any reason, falls back to the raw output so real information is never
// hidden. Two actions at the bottom: Cancel (just collapses the preview,
// nothing sent) and Continue (reveals the existing LIVE confirm step in
// ActionsPanel — real money always stays behind that separate confirmation).

import { useTranslations } from 'next-intl';
import { Badge } from '@/components/ui/Badge';
import { PROMOTE_MAX_IMBALANCE_PCT } from '@/lib/xsec-promote';
import { computePlanBalance, parseRebalancePlan, type RebalancePlanLeg, type XSecJob } from '@/lib/xsec-api';

interface Props {
  job: XSecJob;
  onCancel: () => void;
  onContinue: () => void;
  /** When set, "Continue" is disabled and this reason is shown (used by the promote flow). */
  continueDisabledReason?: string;
}

export function RebalancePreview({ job, onCancel, onContinue, continueDisabledReason }: Props) {
  const t = useTranslations('Xsec');
  const plan = parseRebalancePlan(job.output);

  if (!plan) {
    return (
      <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
        <div className="mono" style={{ fontSize: 11, color: 'var(--tx-dim)' }}>{t('preview_fallback_note')}</div>
        <pre
          className="mono"
          style={{
            margin: 0, fontSize: 11, color: 'var(--tx-hi)', background: 'var(--bg-void)',
            border: '1px solid var(--line)', borderRadius: 4, padding: 10,
            maxHeight: 260, overflow: 'auto', whiteSpace: 'pre-wrap',
          }}
        >
          {job.output || t('monitor_no_output')}
        </pre>
        {previewActions()}
      </div>
    );
  }

  const { longUsd, shortUsd, gross, imbalancePct } = computePlanBalance(plan);
  const balanceWarning = gross > 0 && imbalancePct > PROMOTE_MAX_IMBALANCE_PCT;

  const rows: (RebalancePlanLeg & { action: 'open' | 'close' | 'resize' })[] = [
    ...plan.opens.map((l) => ({ ...l, action: 'open' as const })),
    ...plan.closes.map((l) => ({ ...l, action: 'close' as const })),
    ...plan.resizes.map((l) => ({ ...l, action: 'resize' as const })),
  ];

  function previewActions() {
    const continueDisabled = (plan ? !plan.safetyOk : false) || !!continueDisabledReason;
    return (
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
        <button type="button" onClick={onCancel} style={btnStyle('neutral', false)}>
          {t('btn_preview_cancel')}
        </button>
        <button
          type="button"
          onClick={onContinue}
          disabled={continueDisabled} suppressHydrationWarning
          style={btnStyle('brand', continueDisabled)}
        >
          {t('btn_preview_continue')}
        </button>
        {continueDisabledReason && (
          <div role="alert" data-testid="preview-continue-disabled-reason" className="mono" style={{ flexBasis: '100%', fontSize: 11, color: 'var(--down)' }}>
            {continueDisabledReason}
          </div>
        )}
      </div>
    );
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
      {plan.header && (
        <div className="mono" style={{ fontSize: 12, color: 'var(--tx-hi)' }}>
          {t('preview_summary', {
            legs: plan.header.targetLegs,
            opens: plan.opens.length,
            closes: plan.closes.length,
            resizes: plan.resizes.length,
            perLeg: plan.header.perLegUsd.toFixed(2),
            total: (plan.header.targetLegs * plan.header.perLegUsd).toFixed(2),
          })}
        </div>
      )}

      {gross > 0 && (
        <div
          className="mono"
          style={{
            display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap',
            fontSize: 11, padding: '8px 12px', borderRadius: 'var(--r)',
            border: `1px solid ${balanceWarning ? 'var(--warn)' : 'var(--line)'}`,
            background: balanceWarning ? 'rgba(233,162,62,.07)' : 'transparent',
          }}
        >
          <span style={{ color: 'var(--tx-dim)' }}>
            {t('preview_balance', { long: longUsd.toFixed(2), short: shortUsd.toFixed(2) })}
          </span>
          {balanceWarning && (
            <span style={{ color: 'var(--warn)', fontWeight: 600 }}>
              {t('preview_balance_warning', { pct: imbalancePct.toFixed(1) })}
            </span>
          )}
        </div>
      )}

      {rows.length > 0 && (
        <div style={{ overflowX: 'auto' }}>
          <table className="mono" style={{ width: '100%', borderCollapse: 'collapse', fontSize: 11 }}>
            <thead>
              <tr style={{ color: 'var(--tx-faint)', textAlign: 'left', textTransform: 'uppercase', fontSize: 10 }}>
                <th style={{ padding: '4px 8px' }}>{t('preview_table_pair')}</th>
                <th style={{ padding: '4px 8px' }}>{t('preview_table_side')}</th>
                <th style={{ padding: '4px 8px' }}>{t('preview_table_action')}</th>
                <th style={{ padding: '4px 8px' }}>{t('preview_table_notional')}</th>
                <th style={{ padding: '4px 8px' }}>{t('preview_table_price')}</th>
                <th style={{ padding: '4px 8px' }}>{t('preview_table_contracts')}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r, i) => (
                <tr key={`${r.pair}-${r.action}-${i}`} style={{ borderTop: '1px solid var(--line)' }}>
                  <td style={{ padding: '4px 8px', color: 'var(--tx-hi)' }}>{r.pair}</td>
                  <td style={{ padding: '4px 8px' }}>
                    <Badge variant={r.side === 'buy' ? 'ok' : 'real'}>
                      {r.side === 'buy' ? t('side_long') : t('side_short')}
                    </Badge>
                  </td>
                  <td style={{ padding: '4px 8px', color: actionColor(r.action) }}>{t(`preview_action_${r.action}`)}</td>
                  <td style={{ padding: '4px 8px', color: 'var(--tx-hi)' }}>${r.notionalUsd.toFixed(2)}</td>
                  <td style={{ padding: '4px 8px', color: 'var(--tx-dim)' }}>{r.price != null ? r.price : '—'}</td>
                  <td style={{ padding: '4px 8px', color: 'var(--tx-dim)' }}>
                    {r.price ? (r.notionalUsd / r.price).toFixed(4) : '—'}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {plan.skip.length > 0 && (
        <div style={{ fontSize: 11, color: 'var(--warn)' }}>
          <div style={{ fontWeight: 600, marginBottom: 4 }}>{t('preview_skip_title', { count: plan.skip.length })}</div>
          {plan.skip.map((s) => (
            <div key={s.pair} className="mono" style={{ color: 'var(--tx-dim)' }}>
              {s.pair} — {t('preview_skip_reason', { reason: s.reason })}
            </div>
          ))}
        </div>
      )}

      <div className="mono" style={{ fontSize: 11, color: plan.safetyOk ? 'var(--up)' : 'var(--down)' }}>
        {plan.safetyOk ? t('preview_safety_ok') : t('preview_safety_problems', { count: plan.safetyProblems.length })}
        {!plan.safetyOk && (
          <ul style={{ margin: '4px 0 0', paddingLeft: 18 }}>
            {plan.safetyProblems.map((p, i) => <li key={i}>{p}</li>)}
          </ul>
        )}
      </div>

      {previewActions()}
    </div>
  );
}

function actionColor(action: 'open' | 'close' | 'resize'): string {
  if (action === 'open') return 'var(--up)';
  if (action === 'close') return 'var(--down)';
  return 'var(--warn)';
}

function btnStyle(variant: 'brand' | 'neutral', disabled: boolean): React.CSSProperties {
  const bg = variant === 'brand' ? 'var(--brand)' : 'var(--bg-panel-2)';
  return {
    padding: '9px 18px', borderRadius: 'var(--r)', border: variant === 'neutral' ? '1px solid var(--line)' : 'none',
    background: disabled ? 'var(--bg-void)' : bg,
    color: disabled ? 'var(--tx-faint)' : variant === 'neutral' ? 'var(--tx-hi)' : 'var(--bg)',
    fontSize: 13, fontWeight: 600, cursor: disabled ? 'not-allowed' : 'pointer', whiteSpace: 'nowrap',
  };
}
