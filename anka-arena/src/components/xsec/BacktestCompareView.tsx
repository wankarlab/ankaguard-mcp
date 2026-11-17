'use client';

// BacktestCompareView — side-by-side KPI comparison + overlaid equity curves
// for 2+ selected backtest jobs (Fase 3). Pure frontend, zero new backend —
// every backtest already persists as a row in xsec_jobs; this just re-parses
// each selected job's own real output via the same parsers the single-run
// report already uses.

import { useTranslations } from 'next-intl';
import { parseBacktestReport, parseBacktestEquityCurve, type XSecJob } from '@/lib/xsec-api';
import { MultiEquityChart, type MultiEquitySeries } from '@/components/xsec/MultiEquityChart';

interface Props {
  jobs: XSecJob[];
  onRemove: (jobId: number) => void;
}

const SERIES_COLORS = ['var(--brand)', 'var(--up)', 'var(--down)', 'var(--micro)', 'var(--cyan)'];

function fmtUsd(n: number): string {
  if (!isFinite(n)) return '—';
  return `$${n.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

function fmtPct(n: number): string {
  if (!isFinite(n)) return '—';
  return `${n > 0 ? '+' : ''}${n.toFixed(1)}%`;
}

export function BacktestCompareView({ jobs, onRemove }: Props) {
  const t = useTranslations('Xsec');

  const rows = jobs.map((job) => ({ job, report: parseBacktestReport(job.output) })).filter((r) => r.report != null);
  if (rows.length === 0) return null;

  const series: MultiEquitySeries[] = rows
    .map((r, i) => {
      const points = parseBacktestEquityCurve(r.job.output);
      return points ? { label: `#${r.job.id}`, color: SERIES_COLORS[i % SERIES_COLORS.length], points } : null;
    })
    .filter((s): s is MultiEquitySeries => s != null);

  const kpiRows: { label: string; get: (r: NonNullable<ReturnType<typeof parseBacktestReport>>) => string }[] = [
    { label: t('bt_kpi_final_equity'), get: (r) => fmtUsd(r.finalEquity) },
    { label: t('bt_kpi_total_return'), get: (r) => fmtPct(r.totalReturnPct) },
    { label: t('bt_kpi_cagr'), get: (r) => fmtPct(r.cagrPct) },
    { label: t('bt_kpi_sharpe'), get: (r) => r.sharpe.toFixed(2) },
    { label: t('bt_kpi_max_dd'), get: (r) => `-${r.maxDrawdownPct.toFixed(1)}%` },
    { label: t('bt_kpi_turnover'), get: (r) => `${r.avgTurnoverPct.toFixed(0)}%` },
    { label: t('bt_kpi_funding_net'), get: (r) => fmtUsd(r.fundingNet) },
    { label: t('bt_verdict_label'), get: (r) => r.verdict ?? '—' },
  ];

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      <div className="mono" style={{ fontSize: 10, letterSpacing: '.06em', color: 'var(--tx-faint)', textTransform: 'uppercase' }}>
        {t('compare_title', { n: rows.length })}
      </div>

      {series.length > 0 && <MultiEquityChart series={series} />}

      <div style={{ overflowX: 'auto' }}>
        <table className="mono" style={{ borderCollapse: 'collapse', fontSize: 11, minWidth: '100%' }}>
          <thead>
            <tr>
              <th style={{ padding: '4px 8px', textAlign: 'left', color: 'var(--tx-faint)', fontSize: 10, textTransform: 'uppercase' }} />
              {rows.map((r) => (
                <th key={r.job.id} style={{ padding: '4px 8px', textAlign: 'right' }}>
                  <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: 2 }}>
                    <span style={{ color: 'var(--tx-hi)' }}>#{r.job.id}</span>
                    <button
                      type="button"
                      onClick={() => onRemove(r.job.id)}
                      style={{ background: 'none', border: 'none', color: 'var(--tx-faint)', fontSize: 9, cursor: 'pointer', padding: 0 }}
                    >
                      {t('compare_remove')}
                    </button>
                  </div>
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {kpiRows.map((kr) => (
              <tr key={kr.label} style={{ borderTop: '1px solid var(--line)' }}>
                <td style={{ padding: '4px 8px', color: 'var(--tx-faint)' }}>{kr.label}</td>
                {rows.map((r) => (
                  <td key={r.job.id} style={{ padding: '4px 8px', textAlign: 'right', color: 'var(--tx-hi)' }}>
                    {r.report ? kr.get(r.report) : '—'}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
