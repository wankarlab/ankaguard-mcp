'use client';

// Equity block: current real equity, 24h change and current drawdown, all as
// computed by GET /account/equity-history?range=24h (no client-side numbers).

import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { getAccountEquityHistory, type AccountEquityHistory } from '@/lib/account-api';
import { fmtOrDash } from '@/lib/home-logic';
import { HomeBlock, BlockMessage, Stat } from './HomeBlock';

const usd = (n: number) => `$${n.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
const signed = (n: number) => (n > 0 ? '+' : '') + n.toFixed(2);

export function HomeEquityBlock() {
  const t = useTranslations('Home');
  const { data, error, isLoading } = useSWR<AccountEquityHistory>(
    'home:equity:24h',
    () => getAccountEquityHistory('24h'),
    { revalidateOnFocus: false, refreshInterval: 60_000 },
  );
  const m = data?.metrics ?? null;

  return (
    <HomeBlock title={t('equity_title')} href="/account/equity" linkLabel={t('link_detail')}>
      {isLoading && !data && <BlockMessage text={t('loading')} />}
      {error && !data && <BlockMessage text={t('unavailable')} tone="error" />}
      {data && !m && <BlockMessage text={t('no_data')} />}
      {m && (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(150px, 1fr))', gap: 10 }}>
          <Stat label={t('equity_current')} value={fmtOrDash(m.current, usd)} />
          <Stat
            label={t('equity_change_24h')}
            value={fmtOrDash(m.change_abs, (n) => `${signed(n)} USDT`)}
            sub={fmtOrDash(m.change_pct, (n) => `${signed(n)}%`)}
            color={m.change_abs >= 0 ? 'var(--up)' : 'var(--down)'}
          />
          <Stat
            label={t('equity_drawdown')}
            value={fmtOrDash(m.current_drawdown_pct, (n) => `${n.toFixed(2)}%`)}
            color={m.current_drawdown_pct > 0 ? 'var(--down)' : undefined}
          />
        </div>
      )}
    </HomeBlock>
  );
}
