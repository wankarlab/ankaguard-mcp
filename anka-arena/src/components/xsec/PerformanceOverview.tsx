'use client';

// PerformanceOverview — the "how is the basket actually doing" hero section.
// Every number and the chart here are REAL and live: account equity +
// available margin from GET /portfolio/overview (real KuCoin futures
// account), basket PnL $/%/legs from GET /xsec/status's `totals` (computed
// server-side from xsec_live_book enriched with live exchange PnL), and the
// equity CURVE from GET /xsec/status's `real_equity_history` — hourly
// snapshots of the real account written by internal/tracker.PortfolioTracker
// (portfolio_snapshots table), refreshed on the same 15s poll as everything
// else on this screen.
//
// The xsec_equity table (cmd/xsec-bot's paper simulator, fictitious capital
// since 2026-06-08) intentionally has no presence here — mixing simulated and
// real numbers on the same live dashboard read as real money (see project
// memory 2026-07-23). Its equity curve belongs on a future backtesting/paper
// screen, not this one.
//
// Shares the 'xsec:status' and 'xsec:portfolio-overview' SWR cache keys with
// StatusCard/ActionsPanel so no network request is duplicated.

import { useMemo } from 'react';
import { useTranslations } from 'next-intl';
import useSWR from 'swr';
import { getXsecStatus, listXsecJobs, getXsecFunding, type XSecStatus, type XSecJob, type XSecFunding } from '@/lib/xsec-api';
import { getPortfolioOverview, type PortfolioOverviewLite } from '@/lib/motor-api';
import { EquityChart } from '@/components/xsec/EquityChart';
import { FundingCard } from '@/components/xsec/FundingCard';
import { computeNeutralization } from '@/lib/xsec-neutralize';

// Expected rebalance cadence — the momentum signal decays over ~7-10 days
// (project memory: lookback 10, rebalance 7-10). Past this since the last
// rebalance-live the held basket is likely stale vs a fresh ranking.
const REBALANCE_CADENCE_DAYS = 10;

// Mirrors StatusCard's ADVERSE_PNL_PCT — a leg past this live-PnL fraction is a
// blow-out risk. Kept in sync by hand (both small, single-purpose).
const ADVERSE_PNL_PCT = -15;

type Sev = 'ok' | 'warn' | 'bad';
const SEV_RANK: Record<Sev, number> = { ok: 0, warn: 1, bad: 2 };
interface HealthDim { key: string; sev: Sev; detail: string }

interface EquityStats {
  start: number;
  now: number;
  changeUsd: number;
  changePct: number;
  maxDrawdownPct: number;
  spanDays: number;
}

// Derives the honest after-costs performance of the strategy from the real
// account equity curve (portfolio_snapshots) — that curve already nets fees,
// funding and realized PnL, so its slope IS the true result. No per-leg
// realized ledger exists (xsec closes aren't attributed in trade_history and
// closed book rows are zeroed), so this curve-level view is the truthful one.
function computeEquityStats(points: { ts: string; value: number }[]): EquityStats | null {
  if (points.length < 2) return null;
  const start = points[0].value;
  const now = points[points.length - 1].value;
  if (!isFinite(start) || start <= 0) return null;
  let peak = points[0].value;
  let maxDd = 0;
  for (const p of points) {
    if (p.value > peak) peak = p.value;
    if (peak > 0) maxDd = Math.max(maxDd, (peak - p.value) / peak);
  }
  const t0 = new Date(points[0].ts).getTime();
  const t1 = new Date(points[points.length - 1].ts).getTime();
  const spanDays = isFinite(t0) && isFinite(t1) ? Math.max(0, (t1 - t0) / 86_400_000) : 0;
  return {
    start,
    now,
    changeUsd: now - start,
    changePct: ((now - start) / start) * 100,
    maxDrawdownPct: maxDd * 100,
    spanDays,
  };
}

// Most recent settled rebalance-live job — its finished_at anchors the
// staleness read. Returns null if the basket was never rebalanced via the UI.
function lastRebalanceAt(jobs: XSecJob[] | undefined): Date | null {
  if (!jobs) return null;
  let latest: Date | null = null;
  for (const j of jobs) {
    if (j.mode !== 'rebalance-live' || j.status !== 'done' || !j.finished_at) continue;
    const d = new Date(j.finished_at);
    if (isNaN(d.getTime())) continue;
    if (!latest || d > latest) latest = d;
  }
  return latest;
}

function fmtUsd(n: number, signed = false): string {
  if (!isFinite(n)) return '—';
  const neg = n < 0;
  const abs = Math.abs(n).toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
  const sign = neg ? '-' : signed && n > 0 ? '+' : '';
  return `${sign}$${abs}`;
}

function fmtPct(n: number, signed = false): string {
  if (!isFinite(n)) return '—';
  const sign = signed && n > 0 ? '+' : '';
  return `${sign}${n.toFixed(2)}%`;
}

function fmtTime(iso?: string): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (isNaN(d.getTime()) || d.getUTCFullYear() < 2000) return '—';
  return d.toLocaleString();
}

interface RealMetrics {
  netExposure: number;
  grossExposure: number;
  imbalancePct: number;
}

function computeRealMetrics(data: XSecStatus | undefined): RealMetrics {
  const positions = data?.positions ?? [];
  const netExposure = positions.reduce((s, p) => s + p.dir * p.notional, 0);
  const grossExposure = positions.reduce((s, p) => s + Math.abs(p.notional), 0);
  const imbalancePct = grossExposure > 0 ? (Math.abs(netExposure) / grossExposure) * 100 : 0;
  return { netExposure, grossExposure, imbalancePct };
}

export function PerformanceOverview() {
  const t = useTranslations('Xsec');
  const { data, isLoading, error } = useSWR<XSecStatus>(
    'xsec:status',
    getXsecStatus,
    { revalidateOnFocus: false, refreshInterval: 15_000 },
  );
  const { data: overview } = useSWR<PortfolioOverviewLite>(
    'xsec:portfolio-overview',
    () => getPortfolioOverview(),
    { revalidateOnFocus: false, refreshInterval: 30_000 },
  );

  // Shares the 'xsec:jobs' SWR cache with the page/JobHistory — no extra fetch.
  const { data: jobs } = useSWR<XSecJob[]>('xsec:jobs', () => listXsecJobs(20), {
    revalidateOnFocus: false,
    refreshInterval: 30_000,
  });
  // Shares the 'xsec:funding' key with FundingCard — one fetch feeds both.
  const { data: funding } = useSWR<XSecFunding>('xsec:funding', getXsecFunding, {
    revalidateOnFocus: false,
    refreshInterval: 60_000,
  });

  const m = useMemo(() => computeRealMetrics(data), [data]);
  const advice = useMemo(() => computeNeutralization(data?.positions ?? []), [data]);
  const totals = data?.totals;
  const accountEquity = overview?.futures.account_equity;
  const availableMargin = overview?.futures.available_balance;
  const equityPoints = useMemo(
    () => (data?.real_equity_history ?? []).map((p) => ({ ts: p.ts, value: p.total_equity_usd })),
    [data],
  );
  const eqStats = useMemo(() => computeEquityStats(equityPoints), [equityPoints]);
  const rebalancedAt = useMemo(() => lastRebalanceAt(jobs), [jobs]);
  const daysSinceRebalance = rebalancedAt != null ? (Date.now() - rebalancedAt.getTime()) / 86_400_000 : null;
  const rebalanceStale = daysSinceRebalance != null && daysSinceRebalance > REBALANCE_CADENCE_DAYS;

  // Consolidated health — one badge aggregating the signals already computed.
  // Overall severity = the worst dimension. Dimensions with unknown inputs are
  // simply omitted rather than guessed.
  const health = useMemo(() => {
    const dims: HealthDim[] = [];

    // Neutrality
    dims.push({
      key: 'neutral',
      sev: m.imbalancePct <= 15 ? 'ok' : m.imbalancePct <= 25 ? 'warn' : 'bad',
      detail: fmtPct(m.imbalancePct),
    });

    // Freshness (only when we know the last rebalance)
    if (daysSinceRebalance != null) {
      dims.push({
        key: 'rebalance',
        sev: daysSinceRebalance <= REBALANCE_CADENCE_DAYS ? 'ok'
          : daysSinceRebalance <= REBALANCE_CADENCE_DAYS * 1.5 ? 'warn' : 'bad',
        detail: `${Math.floor(daysSinceRebalance)}d`,
      });
    }

    // Legs at risk (blow-outs)
    const positions = data?.positions ?? [];
    const risky = positions.filter(
      (p) => p.unrealized_pnl != null && p.notional > 0 && (p.unrealized_pnl / p.notional) * 100 <= ADVERSE_PNL_PCT,
    );
    const worst = risky.reduce((mn, p) => Math.min(mn, (p.unrealized_pnl! / p.notional) * 100), 0);
    dims.push({
      key: 'legrisk',
      sev: risky.length === 0 ? 'ok' : worst <= ADVERSE_PNL_PCT * 1.67 ? 'bad' : 'warn',
      detail: risky.length === 0 ? '0' : String(risky.length),
    });

    // Funding drag (only when live rates arrived)
    if (funding && funding.known_legs > 0) {
      dims.push({
        key: 'funding',
        sev: funding.est_daily_usd >= 0 ? 'ok' : 'warn',
        detail: `${funding.est_daily_usd >= 0 ? '+' : '−'}$${Math.abs(funding.est_daily_usd).toFixed(2)}/d`,
      });
    }

    const overall: Sev = dims.reduce<Sev>((acc, d) => (SEV_RANK[d.sev] > SEV_RANK[acc] ? d.sev : acc), 'ok');
    return { overall, dims };
  }, [m.imbalancePct, daysSinceRebalance, data, funding]);

  if (isLoading) {
    return <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)', padding: 16 }}>{t('loading')}</div>;
  }
  if (error) {
    return <div className="mono" style={{ fontSize: 12, color: 'var(--down)', padding: 16 }}>{t('load_error')}</div>;
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {/* Consolidated health — overall status + a chip per dimension. */}
      <div
        style={{
          display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap',
          padding: '8px 12px', borderRadius: 'var(--r)',
          border: `1px solid ${sevColor(health.overall)}`,
          background: sevBg(health.overall),
        }}
      >
        <span style={{ fontSize: 12, fontWeight: 700, color: sevColor(health.overall) }}>
          {t('health_title')}: {t(`health_${health.overall === 'ok' ? 'ok' : health.overall === 'warn' ? 'warn' : 'bad'}`)}
        </span>
        <span style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
          {health.dims.map((d) => (
            <span
              key={d.key}
              className="mono"
              style={{
                fontSize: 10, padding: '2px 7px', borderRadius: 3,
                border: `1px solid ${sevColor(d.sev)}`, color: sevColor(d.sev),
              }}
            >
              {t(`health_dim_${d.key}`)} {d.detail}
            </span>
          ))}
        </span>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(140px, 1fr))', gap: 10 }}>
        <KpiCard label={t('kpi_equity')} value={accountEquity == null ? '—' : fmtUsd(accountEquity)} tone="neutral" />
        <KpiCard label={t('kpi_margin')} value={availableMargin == null ? '—' : fmtUsd(availableMargin)} tone="neutral" />
        <KpiCard
          label={t('kpi_unrealized')}
          value={totals?.total_unrealized_pnl == null ? '—' : fmtUsd(totals.total_unrealized_pnl, true)}
          tone={
            totals?.total_unrealized_pnl == null ? 'neutral'
              : totals.total_unrealized_pnl > 0 ? 'up' : totals.total_unrealized_pnl < 0 ? 'down' : 'neutral'
          }
        />
        <KpiCard
          label={t('kpi_return')}
          value={totals?.total_unrealized_pnl_pct == null ? '—' : fmtPct(totals.total_unrealized_pnl_pct, true)}
          tone={
            totals?.total_unrealized_pnl_pct == null ? 'neutral'
              : totals.total_unrealized_pnl_pct >= 0 ? 'up' : 'down'
          }
        />
        <KpiCard label={t('kpi_legs')} value={t('legs', { long: totals?.n_long ?? 0, short: totals?.n_short ?? 0 })} tone="neutral" />
        <KpiCard
          label={t('kpi_imbalance')}
          value={fmtPct(m.imbalancePct)}
          sub={fmtUsd(m.netExposure, true) + ' ' + t('kpi_net_suffix')}
          tone={m.imbalancePct > 15 ? 'down' : 'neutral'}
        />
      </div>

      {/* Actionable neutralization hint — mirrors the StatusCard advisory but
          here only names the fix (the close buttons live in that table). */}
      {advice && (
        <div
          className="mono"
          style={{
            fontSize: 11, padding: '8px 12px', borderRadius: 'var(--r)',
            border: '1px solid var(--down)', background: 'rgba(233,90,90,.06)', color: 'var(--down)',
          }}
        >
          {t('neutralize_hint_overview', {
            count: advice.suggested.length,
            side: advice.heavySide === 'long' ? t('neutralize_side_long') : t('neutralize_side_short'),
            result: advice.resultingPct.toFixed(1),
          })}
          {' · '}
          <span style={{ color: 'var(--tx-dim)' }}>{advice.suggested.map((p) => p.pair).join(', ')}</span>
        </div>
      )}

      {/* Staleness read — days since the last rebalance-live vs the cadence. */}
      {daysSinceRebalance != null && (
        <div
          className="mono"
          style={{
            fontSize: 11, padding: '6px 12px', borderRadius: 'var(--r)',
            display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap',
            border: `1px solid ${rebalanceStale ? 'var(--warn)' : 'var(--line)'}`,
            background: rebalanceStale ? 'rgba(217,164,65,.06)' : 'transparent',
          }}
        >
          <span style={{ color: 'var(--tx-faint)', textTransform: 'uppercase', letterSpacing: '.04em', fontSize: 10 }}>
            {t('stale_label')}
          </span>
          <span style={{ color: rebalanceStale ? 'var(--warn)' : 'var(--tx-dim)', fontWeight: rebalanceStale ? 600 : 400 }}>
            {rebalanceStale
              ? t('stale_due', { days: Math.floor(daysSinceRebalance), cadence: REBALANCE_CADENCE_DAYS })
              : t('stale_fresh', {
                  days: Math.floor(daysSinceRebalance),
                  hours: Math.floor((daysSinceRebalance % 1) * 24),
                })}
          </span>
        </div>
      )}

      {/* Real after-costs performance derived from the account equity curve. */}
      {eqStats && (
        <div>
          <div className="mono" style={{ fontSize: 10, letterSpacing: '.06em', color: 'var(--tx-faint)', textTransform: 'uppercase', marginBottom: 8 }}>
            {t('eq_stats_title')}
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(110px, 1fr))', gap: 10 }}>
            <MiniStat label={t('eq_stats_start')} value={fmtUsd(eqStats.start)} tone="neutral" />
            <MiniStat label={t('eq_stats_now')} value={fmtUsd(eqStats.now)} tone="neutral" />
            <MiniStat
              label={t('eq_stats_change')}
              value={`${fmtUsd(eqStats.changeUsd, true)} (${fmtPct(eqStats.changePct, true)})`}
              tone={eqStats.changeUsd > 0 ? 'up' : eqStats.changeUsd < 0 ? 'down' : 'neutral'}
            />
            <MiniStat label={t('eq_stats_maxdd')} value={fmtPct(-eqStats.maxDrawdownPct)} tone={eqStats.maxDrawdownPct > 0 ? 'down' : 'neutral'} />
          </div>
          <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', marginTop: 6 }}>
            {t('eq_stats_note')} · {t('eq_stats_period', { days: Math.round(eqStats.spanDays) })}
          </div>
        </div>
      )}

      <div>
        <div className="mono" style={{ fontSize: 10, letterSpacing: '.06em', color: 'var(--tx-faint)', textTransform: 'uppercase', marginBottom: 8 }}>
          {t('equity_curve_label')}
        </div>
        {equityPoints.length < 2 ? (
          <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{t('equity_curve_empty')}</div>
        ) : (
          <EquityChart points={equityPoints} />
        )}
      </div>

      <FundingCard />

      <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', textAlign: 'right' }}>
        {t('updated_at')}: {fmtTime(data?.updated_at)}
      </div>
    </div>
  );
}

function sevColor(s: Sev): string {
  return s === 'ok' ? 'var(--up)' : s === 'warn' ? 'var(--warn)' : 'var(--down)';
}
function sevBg(s: Sev): string {
  return s === 'ok' ? 'rgba(74,219,132,.06)' : s === 'warn' ? 'rgba(217,164,65,.06)' : 'rgba(233,90,90,.06)';
}

function MiniStat({ label, value, tone }: { label: string; value: string; tone: 'up' | 'down' | 'neutral' }) {
  const color = tone === 'up' ? 'var(--up)' : tone === 'down' ? 'var(--down)' : 'var(--tx-hi)';
  return (
    <div style={{ padding: '8px 10px', borderRadius: 'var(--r)', border: '1px solid var(--line)', background: 'var(--bg-inset)' }}>
      <div className="mono" style={{ fontSize: 9, color: 'var(--tx-faint)', textTransform: 'uppercase', letterSpacing: '.04em', marginBottom: 3 }}>
        {label}
      </div>
      <div className="mono" style={{ fontSize: 13, fontWeight: 700, color }}>{value}</div>
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
      <div className="mono" style={{ fontSize: 18, fontWeight: 700, color }}>{value}</div>
      {sub && <div className="mono" style={{ fontSize: 10, color: 'var(--tx-dim)', marginTop: 2 }}>{sub}</div>}
    </div>
  );
}
