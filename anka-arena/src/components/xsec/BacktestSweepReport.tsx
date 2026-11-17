'use client';

// BacktestSweepReport — one-dimensional sensitivity sweep result: a sortable
// table (one row per swept value, each a REAL cmd/xsec-sim run) plus a bar
// row of Sharpe vs the swept value. Same real-data/never-hide-info contract
// as BacktestReport: a run whose report didn't parse still shows its raw
// output via BacktestReport's own fallback.

import { Fragment, useMemo, useState } from 'react';
import { useTranslations } from 'next-intl';
import { Badge } from '@/components/ui/Badge';
import { parseBacktestSweep, type XSecJob, type BacktestSweepRun } from '@/lib/xsec-api';
import { BacktestReport } from '@/components/xsec/BacktestReport';

interface Props {
  job: XSecJob;
}

type SortKey = 'sharpe' | 'cagr' | 'totalReturn' | 'maxDD' | 'paramValue';

function fmtPct(n: number, signed = false): string {
  if (!isFinite(n)) return '—';
  const sign = signed && n > 0 ? '+' : '';
  return `${sign}${n.toFixed(1)}%`;
}

export function BacktestSweepReport({ job }: Props) {
  const t = useTranslations('Xsec');
  const runs = parseBacktestSweep(job.output);
  const [sortKey, setSortKey] = useState<SortKey>('sharpe');
  const [expanded, setExpanded] = useState<number | null>(null);
  const [copiedIndex, setCopiedIndex] = useState<number | null>(null);

  function copyConfig(r: BacktestSweepRun) {
    const text = `${r.param}=${r.paramValue}`;
    void navigator.clipboard?.writeText(text).then(() => {
      setCopiedIndex(r.index);
      setTimeout(() => setCopiedIndex((i) => (i === r.index ? null : i)), 2000);
    });
  }

  const sorted = useMemo(() => {
    if (!runs) return [];
    const withReport = [...runs];
    withReport.sort((a, b) => {
      if (sortKey === 'paramValue') return a.paramValue - b.paramValue;
      const av = a.report ? sortValue(a.report, sortKey) : -Infinity;
      const bv = b.report ? sortValue(b.report, sortKey) : -Infinity;
      return bv - av;
    });
    return withReport;
  }, [runs, sortKey]);

  if (!runs) {
    return (
      <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
        <div className="mono" style={{ fontSize: 11, color: 'var(--tx-dim)' }}>{t('backtest_fallback_note')}</div>
        <pre
          className="mono"
          style={{
            margin: 0, fontSize: 11, color: 'var(--tx-hi)', background: 'var(--bg-void)',
            border: '1px solid var(--line)', borderRadius: 4, padding: 10,
            maxHeight: 320, overflow: 'auto', whiteSpace: 'pre-wrap',
          }}
        >
          {job.output || t('monitor_no_output')}
        </pre>
      </div>
    );
  }

  const bestIndex = sorted.findIndex((r) => r.report != null);
  const best = bestIndex >= 0 ? sorted[bestIndex] : null;
  const maxAbsSharpe = Math.max(1, ...runs.filter((r) => r.report).map((r) => Math.abs(r.report!.sharpe)));

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      <div className="mono" style={{ fontSize: 11, color: 'var(--tx-dim)' }}>
        {t('sweep_summary', { n: runs.length, ok: runs.filter((r) => r.report).length })}
      </div>

      <div>
        <div className="mono" style={{ fontSize: 10, letterSpacing: '.06em', color: 'var(--tx-faint)', textTransform: 'uppercase', marginBottom: 8 }}>
          {t('sweep_sharpe_by_value_label')}
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          {[...runs].sort((a, b) => a.paramValue - b.paramValue).map((r) => {
            const sharpe = r.report?.sharpe ?? 0;
            const widthPct = r.report ? (Math.abs(sharpe) / maxAbsSharpe) * 100 : 0;
            const up = sharpe >= 0;
            return (
              <div key={r.index} style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <span className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', width: 32 }}>{r.paramValue}</span>
                <div style={{ flex: 1, height: 14, background: 'var(--bg-inset)', borderRadius: 3, position: 'relative', overflow: 'hidden' }}>
                  {r.report && (
                    <div
                      style={{
                        position: 'absolute', top: 0, bottom: 0,
                        left: up ? '50%' : `${50 - widthPct / 2}%`,
                        width: `${widthPct / 2}%`,
                        background: up ? 'var(--up)' : 'var(--down)',
                      }}
                    />
                  )}
                  <div style={{ position: 'absolute', left: '50%', top: 0, bottom: 0, width: 1, background: 'var(--line)' }} />
                </div>
                <span className="mono" style={{ fontSize: 11, width: 48, textAlign: 'right', color: r.report ? (up ? 'var(--up)' : 'var(--down)') : 'var(--tx-faint)' }}>
                  {r.report ? sharpe.toFixed(2) : '—'}
                </span>
              </div>
            );
          })}
        </div>
      </div>

      <div style={{ overflowX: 'auto' }}>
        <table className="mono" style={{ width: '100%', borderCollapse: 'collapse', fontSize: 11 }}>
          <thead>
            <tr style={{ color: 'var(--tx-faint)', textAlign: 'left', textTransform: 'uppercase', fontSize: 10 }}>
              <th style={{ padding: '4px 8px' }}>
                <SortHeader label={t('sweep_col_value')} active={sortKey === 'paramValue'} onClick={() => setSortKey('paramValue')} />
              </th>
              <th style={{ padding: '4px 8px', textAlign: 'right' }}>
                <SortHeader label={t('bt_kpi_sharpe')} active={sortKey === 'sharpe'} onClick={() => setSortKey('sharpe')} />
              </th>
              <th style={{ padding: '4px 8px', textAlign: 'right' }}>
                <SortHeader label={t('bt_kpi_cagr')} active={sortKey === 'cagr'} onClick={() => setSortKey('cagr')} />
              </th>
              <th style={{ padding: '4px 8px', textAlign: 'right' }}>
                <SortHeader label={t('bt_kpi_total_return')} active={sortKey === 'totalReturn'} onClick={() => setSortKey('totalReturn')} />
              </th>
              <th style={{ padding: '4px 8px', textAlign: 'right' }}>
                <SortHeader label={t('bt_kpi_max_dd')} active={sortKey === 'maxDD'} onClick={() => setSortKey('maxDD')} />
              </th>
              <th style={{ padding: '4px 8px' }} />
            </tr>
          </thead>
          <tbody>
            {sorted.map((r) => (
              <Fragment key={r.index}>
                <tr style={{ borderTop: '1px solid var(--line)' }}>
                  <td style={{ padding: '4px 8px', color: 'var(--tx-hi)' }}>
                    {r.paramValue}
                    {best && r.index === best.index && (
                      <Badge variant="ok" style={{ marginLeft: 6 }}>{t('sweep_best_badge')}</Badge>
                    )}
                  </td>
                  {r.report ? (
                    <>
                      <td style={{ padding: '4px 8px', textAlign: 'right', color: 'var(--tx-hi)' }}>{r.report.sharpe.toFixed(2)}</td>
                      <td style={{ padding: '4px 8px', textAlign: 'right', color: r.report.cagrPct >= 0 ? 'var(--up)' : 'var(--down)' }}>{fmtPct(r.report.cagrPct, true)}</td>
                      <td style={{ padding: '4px 8px', textAlign: 'right', color: r.report.totalReturnPct >= 0 ? 'var(--up)' : 'var(--down)' }}>{fmtPct(r.report.totalReturnPct, true)}</td>
                      <td style={{ padding: '4px 8px', textAlign: 'right', color: 'var(--tx-dim)' }}>-{r.report.maxDrawdownPct.toFixed(1)}%</td>
                    </>
                  ) : (
                    <td colSpan={4} style={{ padding: '4px 8px', color: 'var(--down)' }}>{t('sweep_run_failed')}</td>
                  )}
                  <td style={{ padding: '4px 8px', display: 'flex', gap: 6 }}>
                    <button type="button" onClick={() => setExpanded(expanded === r.index ? null : r.index)} style={linkBtnStyle}>
                      {expanded === r.index ? t('sweep_hide_detail') : t('sweep_show_detail')}
                    </button>
                    {r.report && (
                      <button type="button" onClick={() => copyConfig(r)} style={linkBtnStyle}>
                        {copiedIndex === r.index ? t('sweep_config_copied') : t('sweep_use_config')}
                      </button>
                    )}
                  </td>
                </tr>
                {expanded === r.index && (
                  <tr>
                    <td colSpan={6} style={{ padding: '10px 8px', background: 'var(--bg-inset)', borderRadius: 'var(--r)' }}>
                      <BacktestReport job={{ ...job, output: r.rawOutput }} />
                    </td>
                  </tr>
                )}
              </Fragment>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function sortValue(report: BacktestSweepRun['report'], key: SortKey): number {
  if (!report) return -Infinity;
  switch (key) {
    case 'sharpe': return report.sharpe;
    case 'cagr': return report.cagrPct;
    case 'totalReturn': return report.totalReturnPct;
    case 'maxDD': return -report.maxDrawdownPct; // lower drawdown sorts first when "sorting by maxDD"
    default: return 0;
  }
}

function SortHeader({ label, active, onClick }: { label: string; active: boolean; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      style={{
        background: 'none', border: 'none', padding: 0, cursor: 'pointer',
        color: active ? 'var(--brand)' : 'var(--tx-faint)', fontSize: 10, textTransform: 'uppercase',
        fontFamily: 'var(--mono)', fontWeight: active ? 700 : 400,
      }}
    >
      {label}{active ? ' ▾' : ''}
    </button>
  );
}

const linkBtnStyle: React.CSSProperties = {
  background: 'none', border: '1px solid var(--line)', borderRadius: 4, padding: '3px 8px',
  color: 'var(--tx-dim)', fontSize: 10, cursor: 'pointer', whiteSpace: 'nowrap',
};
