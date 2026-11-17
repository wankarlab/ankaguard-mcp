'use client';

// BacktestReport — structured view of a backtest job's real cmd/xsec-sim
// output (real historical candle_archive data, nothing simulated
// client-side). Parses the stdout report via parseBacktestReport; if parsing
// fails for any reason, falls back to the raw output so real information is
// never hidden — same contract RebalancePreview uses for the dry-run plan.

import { useTranslations } from 'next-intl';
import { Badge } from '@/components/ui/Badge';
import { parseBacktestReport, parseBacktestEquityCurve, type XSecJob } from '@/lib/xsec-api';
import { EquityChart } from '@/components/xsec/EquityChart';

interface Props {
  job: XSecJob;
}

function fmtUsd(n: number): string {
  if (!isFinite(n)) return '—';
  const neg = n < 0;
  const abs = Math.abs(n).toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
  return `${neg ? '-' : ''}$${abs}`;
}

function fmtPct(n: number, signed = false): string {
  if (!isFinite(n)) return '—';
  const sign = signed && n > 0 ? '+' : '';
  return `${sign}${n.toFixed(1)}%`;
}

export function BacktestReport({ job }: Props) {
  const t = useTranslations('Xsec');
  const report = parseBacktestReport(job.output);

  if (!report) {
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

  const verdictVariant = report.verdict === 'PROMISING' ? 'ok' : report.verdict === 'NEGATIVE' ? 'real' : 'micro';
  const maxAbsWindowReturn = Math.max(1, ...report.windows.map((w) => Math.abs(w.returnPct)));
  const equityPoints = parseBacktestEquityCurve(job.output);

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      {(report.timeframe || report.pairs != null) && (
        <div className="mono" style={{ fontSize: 11, color: 'var(--tx-dim)' }}>
          {t('backtest_config_summary', {
            tf: report.timeframe ?? '—',
            pairs: report.pairs ?? 0,
            years: (report.years ?? 0).toFixed(1),
          })}
        </div>
      )}

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(130px, 1fr))', gap: 10 }}>
        <KpiCard label={t('bt_kpi_final_equity')} value={fmtUsd(report.finalEquity)} sub={t('bt_kpi_start', { v: fmtUsd(report.startCapital) })} tone="neutral" />
        <KpiCard label={t('bt_kpi_total_return')} value={fmtPct(report.totalReturnPct, true)} tone={report.totalReturnPct >= 0 ? 'up' : 'down'} />
        <KpiCard label={t('bt_kpi_cagr')} value={fmtPct(report.cagrPct, true)} tone={report.cagrPct >= 0 ? 'up' : 'down'} />
        <KpiCard label={t('bt_kpi_sharpe')} value={report.sharpe.toFixed(2)} tone={report.sharpe >= 1 ? 'up' : report.sharpe >= 0 ? 'neutral' : 'down'} />
        <KpiCard label={t('bt_kpi_max_dd')} value={`-${report.maxDrawdownPct.toFixed(1)}%`} tone={report.maxDrawdownPct > 25 ? 'down' : 'neutral'} />
        <KpiCard
          label={t('bt_kpi_turnover')}
          value={`${report.avgTurnoverPct.toFixed(0)}%`}
          sub={t('bt_kpi_rebalances', { n: report.rebalances })}
          tone="neutral"
        />
        <KpiCard label={t('bt_kpi_funding_net')} value={fmtUsd(report.fundingNet)} tone={report.fundingNet >= 0 ? 'up' : 'down'} />
      </div>

      {report.liquidated === true && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
          <Badge variant="real" dot>
            {report.liquidatedAt
              ? t('bt_liquidated_on', { date: report.liquidatedAt })
              : t('bt_liquidated')}
          </Badge>
          <span className="mono" style={{ fontSize: 11, color: 'var(--tx-dim)' }}>{t('bt_liquidated_hint')}</span>
        </div>
      )}

      {report.verdict && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
          <Badge variant={verdictVariant}>{t('bt_verdict_label')}: {report.verdict}</Badge>
          <span className="mono" style={{ fontSize: 11, color: 'var(--tx-dim)' }}>{report.verdictDetail}</span>
        </div>
      )}

      <Interpretation report={report} t={t} />


      {equityPoints && (
        <div>
          <div className="mono" style={{ fontSize: 10, letterSpacing: '.06em', color: 'var(--tx-faint)', textTransform: 'uppercase', marginBottom: 8 }}>
            {t('bt_equity_curve_label')}
          </div>
          <EquityChart points={equityPoints} />
        </div>
      )}

      {report.windows.length > 0 && (
        <div>
          <div className="mono" style={{ fontSize: 10, letterSpacing: '.06em', color: 'var(--tx-faint)', textTransform: 'uppercase', marginBottom: 8 }}>
            {t('bt_windows_label')}
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            {report.windows.map((w) => {
              const widthPct = (Math.abs(w.returnPct) / maxAbsWindowReturn) * 100;
              const up = w.returnPct >= 0;
              return (
                <div key={w.index} style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <span className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', width: 28 }}>W{w.index}</span>
                  <div style={{ flex: 1, height: 14, background: 'var(--bg-inset)', borderRadius: 3, position: 'relative', overflow: 'hidden' }}>
                    <div
                      style={{
                        position: 'absolute', top: 0, bottom: 0,
                        left: up ? '50%' : `${50 - widthPct / 2}%`,
                        width: `${widthPct / 2}%`,
                        background: up ? 'var(--up)' : 'var(--down)',
                      }}
                    />
                    <div style={{ position: 'absolute', left: '50%', top: 0, bottom: 0, width: 1, background: 'var(--line)' }} />
                  </div>
                  <span className="mono" style={{ fontSize: 11, width: 56, textAlign: 'right', color: up ? 'var(--up)' : 'var(--down)' }}>
                    {fmtPct(w.returnPct, true)}
                  </span>
                  <span className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', width: 90 }}>
                    Sharpe {w.sharpe.toFixed(2)}
                  </span>
                  {w.liquidated && <Badge variant="real">{t('bt_liquidated')}</Badge>}
                </div>
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
}

// Interpretation — turns the raw KPIs into a plain-language semaphore so an
// operator who doesn't read Sharpe/DD fluently still gets a verdict per
// dimension. Pure derivation from the parsed report; thresholds mirror the
// research conventions used across the xsec memory (Sharpe≥1.3 solid, DD≤12%
// low, ≥75% of walk-forward windows positive = consistent).
type Tier = 'good' | 'ok' | 'bad';
const TIER_DOT: Record<Tier, string> = { good: '🟢', ok: '🟡', bad: '🔴' };

function Interpretation({ report, t }: { report: NonNullable<ReturnType<typeof parseBacktestReport>>; t: ReturnType<typeof useTranslations> }) {
  const sharpeTier: Tier = report.sharpe >= 1.3 ? 'good' : report.sharpe >= 0.5 ? 'ok' : 'bad';
  const ddTier: Tier = report.maxDrawdownPct <= 12 ? 'good' : report.maxDrawdownPct <= 25 ? 'ok' : 'bad';
  const cagrTier: Tier = report.cagrPct >= 20 ? 'good' : report.cagrPct >= 0 ? 'ok' : 'bad';

  const nWin = report.windows.length;
  const posFrac = nWin > 0 ? report.windows.filter((w) => w.returnPct >= 0).length / nWin : 0;
  const consTier: Tier = posFrac >= 0.75 ? 'good' : posFrac >= 0.5 ? 'ok' : 'bad';

  const rows: { dim: string; tier: Tier; detail: string }[] = [
    { dim: t('interp_sharpe'), tier: sharpeTier, detail: t(`tier_sharpe_${sharpeTier}`, { v: report.sharpe.toFixed(2) }) },
    { dim: t('interp_dd'), tier: ddTier, detail: t(`tier_dd_${ddTier}`, { v: report.maxDrawdownPct.toFixed(1) }) },
    { dim: t('interp_return'), tier: cagrTier, detail: t(`tier_return_${cagrTier}`, { v: report.cagrPct.toFixed(1) }) },
  ];
  if (nWin > 0) {
    rows.push({ dim: t('interp_consistency'), tier: consTier, detail: t(`tier_cons_${consTier}`, { pos: Math.round(posFrac * 100), n: nWin }) });
  }

  const bad = rows.filter((r) => r.tier === 'bad').length;
  const good = rows.filter((r) => r.tier === 'good').length;
  const overall: Tier = bad > 0 ? 'bad' : good >= rows.length - 1 ? 'good' : 'ok';

  return (
    <div style={{ padding: 12, borderRadius: 'var(--r)', border: '1px solid var(--line)', background: 'var(--bg-inset)', display: 'flex', flexDirection: 'column', gap: 8 }}>
      <div className="mono" style={{ fontSize: 10, letterSpacing: '.06em', color: 'var(--tx-faint)', textTransform: 'uppercase' }}>
        {t('interp_title')}
      </div>
      {rows.map((r) => (
        <div key={r.dim} style={{ display: 'flex', alignItems: 'baseline', gap: 8 }}>
          <span style={{ fontSize: 12 }}>{TIER_DOT[r.tier]}</span>
          <span className="mono" style={{ fontSize: 11, color: 'var(--tx-hi)', width: 96, flexShrink: 0 }}>{r.dim}</span>
          <span className="mono" style={{ fontSize: 11, color: 'var(--tx-dim)' }}>{r.detail}</span>
        </div>
      ))}
      <div className="mono" style={{ fontSize: 11, color: 'var(--tx-hi)', fontWeight: 600, marginTop: 2 }}>
        {TIER_DOT[overall]} {t(`interp_overall_${overall}`)}
      </div>
    </div>
  );
}

function KpiCard({ label, value, sub, tone }: { label: string; value: string; sub?: string; tone: 'up' | 'down' | 'neutral' }) {
  const color = tone === 'up' ? 'var(--up)' : tone === 'down' ? 'var(--down)' : 'var(--tx-hi)';
  return (
    <div style={{ padding: '10px 12px', borderRadius: 'var(--r)', border: '1px solid var(--line)', background: 'var(--bg-inset)' }}>
      <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', textTransform: 'uppercase', letterSpacing: '.04em', marginBottom: 4 }}>
        {label}
      </div>
      <div className="mono" style={{ fontSize: 16, fontWeight: 700, color }}>{value}</div>
      {sub && <div className="mono" style={{ fontSize: 10, color: 'var(--tx-dim)', marginTop: 2 }}>{sub}</div>}
    </div>
  );
}
