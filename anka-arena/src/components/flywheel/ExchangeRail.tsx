'use client';

import useSWR from 'swr';
import clsx from 'clsx';
import { useTranslations } from 'next-intl';
import { apiFetch } from '@/lib/api';
import { toExchanges, type ExchangeInfo } from '@/lib/adapters';

export type { ExchangeInfo };

// ──────────────────────────────────────────────────────────────
//  Fetcher — unwrap backend envelope { data:{ exchanges:[{name,status}] } }
// ──────────────────────────────────────────────────────────────

export async function fetchExchanges(): Promise<ExchangeInfo[]> {
  return toExchanges(await apiFetch<unknown>('/api/v1/exchanges'));
}

// ──────────────────────────────────────────────────────────────
//  Fallback when API is unavailable
// ──────────────────────────────────────────────────────────────

const FALLBACK_EXCHANGES: ExchangeInfo[] = [
  { name: 'KuCoin',  enabled: true,  sandbox: false, connected: true },
  { name: 'Binance', enabled: false, sandbox: false, connected: false },
];

// ──────────────────────────────────────────────────────────────
//  Sub-component: single row
// ──────────────────────────────────────────────────────────────

function ExchangeRow({ ex }: { ex: ExchangeInfo }) {
  const isLive = ex.enabled && ex.connected;

  return (
    <div
      className="panel-inset"
      style={{
        display:    'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        padding:    '10px 13px',
        opacity:    ex.enabled ? 1 : 0.55,
      }}
    >
      {/* Name + dot */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 9 }}>
        {/* Live dot */}
        <span
          style={{
            width:        8,
            height:       8,
            borderRadius: '50%',
            flexShrink:   0,
            background:   isLive ? 'var(--up)' : 'var(--tx-faint)',
            boxShadow:    isLive ? '0 0 8px var(--up)' : 'none',
          }}
          className={clsx(isLive && 'fw-dot-live')}
        />
        <b style={{ color: isLive ? 'var(--tx-hi)' : 'var(--tx)', fontWeight: 600, fontSize: 13 }}>
          {ex.name}
        </b>
      </div>

      {/* Status badge */}
      {isLive ? (
        <span className="badge real" style={{ fontSize: 10 }}>
          <span
            className="bd-dot"
            style={{ background: 'var(--up)', boxShadow: '0 0 6px var(--up)' }}
          />
          LIVE{ex.sandbox ? ' · sandbox' : ' · sandbox:false'}
        </span>
      ) : (
        <span className="badge neutral" style={{ fontSize: 10 }}>
          {ex.enabled ? 'OFFLINE' : 'enabled: false'}
        </span>
      )}
    </div>
  );
}

// ──────────────────────────────────────────────────────────────
//  Main component
// ──────────────────────────────────────────────────────────────

interface ExchangeRailProps {
  className?: string;
}

export function ExchangeRail({ className }: ExchangeRailProps) {
  const t = useTranslations('UiFlywheel');
  const { data, error } = useSWR<ExchangeInfo[]>(
    '/api/v1/exchanges',
    fetchExchanges,
    { refreshInterval: 30_000, fallbackData: FALLBACK_EXCHANGES },
  );

  const exchanges = data ?? FALLBACK_EXCHANGES;

  return (
    <div className={clsx(className)} style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
      {exchanges.map((ex) => (
        <ExchangeRow key={ex.name} ex={ex} />
      ))}

      {error && (
        <p
          style={{
            fontSize:   10,
            color:      'var(--tx-faint)',
            fontFamily: 'var(--mono)',
            margin:     0,
          }}
        >
          {t('exchanges_error')}
        </p>
      )}

      {/* SOR note */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 2 }}>
        <span
          style={{
            fontSize:      10,
            color:         'var(--tx-faint)',
            fontFamily:    'var(--mono)',
            background:    'var(--bg-inset)',
            border:        '1px solid var(--line)',
            borderRadius:  4,
            padding:       '2px 7px',
          }}
        >
          MOON
        </span>
        <span className="dim" style={{ fontSize: 10.5, color: 'var(--tx-dim)' }}>
          {t('sor_note')}
        </span>
      </div>
    </div>
  );
}
