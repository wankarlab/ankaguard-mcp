'use client';

import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { apiFetch } from '@/lib/api';
import { toEngineStats } from '@/lib/adapters';
import type { EngineStats } from '@/stores/engine';
import { fetchExchanges } from '@/components/flywheel/ExchangeRail';
import type { ExchangeInfo } from '@/lib/adapters';
import { isPlatformMode } from '@/lib/consoleMode';
import { isProductEdition } from '@/lib/productMode';
import { LocaleSwitcher } from '@/components/ui/LocaleSwitcher';

// SWR fetcher — unwrap the backend envelope into the FE EngineStats shape.
const fetcher = async (path: string) =>
  toEngineStats(await apiFetch<unknown>(path)) as Partial<EngineStats>;

function fmt(n: number, decimals = 2): string {
  if (!isFinite(n)) return '—';
  return n.toFixed(decimals);
}

function fmtUsd(n: number): string {
  if (!isFinite(n)) return '—';
  return `$${Math.abs(n).toLocaleString('en-US', { minimumFractionDigits: 0, maximumFractionDigits: 0 })}`;
}

export function Ticker() {
  const t = useTranslations('UiTicker');
  // Platform metrics (AUM-cert, edges, gate, GBM PF) only in platform mode.
  const platform = isPlatformMode();
  const { data, error, isLoading } = useSWR<Partial<EngineStats>>(
    platform ? '/api/v1/engine/stats' : null,
    fetcher,
    {
      refreshInterval: 30_000,
      revalidateOnFocus: true,
      dedupingInterval: 10_000,
    },
  );

  // Exchange status is derived from /api/v1/exchanges (same key + fetcher as ExchangeRail).
  const { data: exchanges, error: exError, isLoading: exLoading } = useSWR<ExchangeInfo[]>(
    '/api/v1/exchanges',
    fetchExchanges,
    { refreshInterval: 30_000 },
  );
  const exPending = exLoading && !exchanges && !exError;
  const kucoinUp  = !exError && !!exchanges?.some((e) => e.name.toLowerCase() === 'kucoin' && e.connected);
  const isLive     = kucoinUp;
  // In the product "LIVE" read as real money while live trading may be off:
  // there it only states whether the exchange is reachable.
  const product    = isProductEdition();
  const liveLabel  = product ? t('product_connected') : 'LIVE';
  const offLabel   = product ? t('product_disconnected') : 'OFFLINE';

  return (
    <div className="tb-ticker" aria-label={t('engine_status')}>
      {/* Live status dot */}
      <div className="tk" title={product ? t('product_connection_title') : undefined}>
        <span
          className="tk-dot live-dot"
          style={{
            background: exPending ? 'var(--tx-faint)' : isLive ? 'var(--ok)' : 'var(--bad)',
          }}
        />
        <span style={{ color: exPending ? 'var(--tx-faint)' : isLive ? 'var(--ok)' : 'var(--bad)' }}>
          KuCoin
        </span>
        <b>{exPending ? '…' : isLive ? liveLabel : offLabel}</b>
      </div>

      {platform && (<>
      <div className="tk">
        <span style={{ color: 'var(--tx-faint)' }}>AUM-cert</span>
        <b>{isLoading ? '…' : fmtUsd(data?.aumCertUsd ?? 0)}</b>
      </div>

      <div className="tk">
        <span style={{ color: 'var(--tx-faint)' }}>edges</span>
        <b>
          {isLoading
            ? '…'
            : `${data?.edgesCertified ?? 0}/${data?.edgesTotal ?? 0}`}
        </b>
      </div>

      <div className="tk">
        <span style={{ color: 'var(--tx-faint)' }}>gate</span>
        <b>
          {isLoading ? '…' : `${fmt(data?.gatePassPct ?? 0, 0)}%`}
        </b>
      </div>

      <div className="tk">
        <span style={{ color: 'var(--tx-faint)' }}>GBM PF</span>
        <b
          style={{
            color:
              (data?.gbmPf ?? 0) >= 1.3
                ? 'var(--ok)'
                : (data?.gbmPf ?? 0) >= 1.0
                ? 'var(--warn)'
                : 'var(--bad)',
          }}
        >
          {isLoading ? '…' : fmt(data?.gbmPf ?? 0)}
        </b>
      </div>

      </>)}

      {platform && data?.mlServerUp === false && (
        <div className="tk">
          <span
            style={{
              fontFamily: 'var(--mono)',
              fontSize: '10px',
              color: 'var(--warn)',
              border: '1px solid rgba(233,162,62,0.3)',
              borderRadius: '4px',
              padding: '1px 6px',
            }}
          >
            ML DOWN
          </span>
        </div>
      )}

      {error && (
        <div className="tk">
          <span style={{ color: 'var(--tx-faint)', fontSize: '10px' }}>
            {t('no_data')}
          </span>
        </div>
      )}

      {/* Language Switcher injected into the topbar */}
      <LocaleSwitcher />
    </div>
  );
}
