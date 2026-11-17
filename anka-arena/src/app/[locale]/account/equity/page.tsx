'use client';

// /account/equity — REAL whole-account equity (hourly portfolio snapshots).
// Distinct from the XSEC paper curves: no simulation badge, labelled as real
// account data. Empty/error/loading states come from the backend response.

import { useState } from 'react';
import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { Panel, PanelHeader, PanelBody } from '@/components/ui/Panel';
import { Badge } from '@/components/ui/Badge';
import { EquityChart } from '@/components/xsec/EquityChart';
import {
  EQUITY_RANGES,
  getAccountEquityHistory,
  type AccountEquityHistory,
  type EquityRange,
} from '@/lib/account-api';

const usd = (n: number) => `$${n.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
const pct = (n: number) => `${n.toFixed(2)}%`;

export default function AccountEquityPage() {
  const t = useTranslations('AccountEquity');
  const [range, setRange] = useState<EquityRange>('7d');

  const { data, error, isLoading } = useSWR<AccountEquityHistory>(
    `account:equity:${range}`,
    () => getAccountEquityHistory(range),
    { revalidateOnFocus: false, refreshInterval: 60_000 },
  );

  const points = data?.points ?? [];
  const m = data?.metrics ?? null;
  const signed = (n: number) => (n > 0 ? '+' : '') + n.toFixed(2);

  return (
    <div className="content-pad" style={{ maxWidth: 1000, margin: '0 auto' }}>
      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', flexWrap: 'wrap', gap: 10, marginBottom: 20 }}>
        <div>
          <div className="mono" style={{ fontSize: 10, letterSpacing: '.08em', color: 'var(--brand)', textTransform: 'uppercase', marginBottom: 4 }}>
            {t('eyebrow')}
          </div>
          <h2 style={{ margin: 0, fontSize: 20, color: 'var(--tx-hi)', fontWeight: 700 }}>{t('title')}</h2>
        </div>
        <Badge variant="ok">{t('badge_real')}</Badge>
      </div>

      <Panel>
        <PanelHeader title={t('chart_title')} sub={t('chart_sub')}>
          <div role="group" aria-label={t('range_label')} style={{ display: 'flex', gap: 6 }}>
            {EQUITY_RANGES.map((r) => (
              <button
                key={r}
                type="button"
                aria-pressed={range === r}
                onClick={() => setRange(r)}
                style={{
                  padding: '4px 10px', borderRadius: 'var(--r)', fontSize: 11, fontWeight: 600, cursor: 'pointer',
                  border: range === r ? '1px solid var(--brand)' : '1px solid var(--line)',
                  background: range === r ? 'rgba(157,124,255,.1)' : 'transparent',
                  color: range === r ? 'var(--brand)' : 'var(--tx-dim)',
                }}
              >
                {t(`range_${r}`)}
              </button>
            ))}
          </div>
        </PanelHeader>
        <PanelBody>
          {isLoading && !data && (
            <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)', padding: 16 }}>{t('loading')}</div>
          )}
          {error && !data && (
            <div className="mono" style={{ fontSize: 12, color: 'var(--down)', padding: 16 }}>{t('load_error')}</div>
          )}
          {data && points.length === 0 && (
            <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)', padding: 16 }}>{t('empty')}</div>
          )}
          {data && points.length > 0 && (
            <>
              <EquityChart points={points.map((p) => ({ ts: p.ts, value: p.total_equity_usd }))} />
              <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', marginTop: 6 }}>
                {data.reduced
                  ? t('points_reduced', { shown: points.length, total: data.total_points })
                  : t('points_count', { count: points.length })}
              </div>
            </>
          )}
        </PanelBody>
      </Panel>

      {m && (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(170px, 1fr))', gap: 12, marginTop: 16 }}>
          <Metric label={t('m_current')} value={usd(m.current)} />
          <Metric label={t('m_peak')} value={usd(m.peak)} />
          <Metric
            label={t('m_change')}
            value={`${signed(m.change_abs)} USDT`}
            sub={m.change_pct == null ? t('change_pct_na') : `${signed(m.change_pct)}%`}
            tone={m.change_abs >= 0 ? 'up' : 'down'}
          />
          <Metric label={t('m_max_dd')} value={pct(m.max_drawdown_pct)} tone={m.max_drawdown_pct > 0 ? 'down' : undefined} />
          <Metric label={t('m_current_dd')} value={pct(m.current_drawdown_pct)} tone={m.current_drawdown_pct > 0 ? 'down' : undefined} />
        </div>
      )}
    </div>
  );
}

function Metric({ label, value, sub, tone }: { label: string; value: string; sub?: string; tone?: 'up' | 'down' }) {
  const color = tone === 'up' ? 'var(--up)' : tone === 'down' ? 'var(--down)' : 'var(--tx-hi)';
  return (
    <div className="panel-inset" style={{ padding: '12px 14px' }}>
      <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', textTransform: 'uppercase', letterSpacing: '.06em' }}>{label}</div>
      <div className="mono" style={{ fontSize: 18, fontWeight: 700, color, marginTop: 4 }}>{value}</div>
      {sub && <div className="mono" style={{ fontSize: 11, color, marginTop: 2 }}>{sub}</div>}
    </div>
  );
}
