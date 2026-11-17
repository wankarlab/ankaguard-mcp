'use client';

/**
 * /arena — Tournament index page.
 * Fetches active tournaments via SWR, redirects to /arena/[id] if one is active.
 */

import { useEffect } from 'react';
import { useRouter } from '@/i18n/routing';
import { useTranslations } from 'next-intl';
import useSWR from 'swr';
import { api } from '@/lib/api';
import { Spinner } from '@/components/ui/Spinner';
import { color } from '@/design-system/tokens';

interface TournamentSummary {
  id:         string;
  name:       string;
  status:     'LOBBY' | 'ACTIVE' | 'VOTING' | 'SETTLE' | 'RECAP' | 'ENDED';
  prize_pool: number;
  currency:   string;
  start_at:   number;
  end_at:     number;
}

interface TournamentsResponse {
  tournaments: TournamentSummary[];
}

export default function ArenaIndexPage() {
  const router = useRouter();
  const t = useTranslations('Arena');

  const { data, isLoading, error } = useSWR<TournamentsResponse>(
    '/api/v1/tournament',
    // Backend returns the list directly under `data` (already unwrapped by apiFetch).
    // Normalize to { tournaments } so an array or a wrapped object both work.
    async (path: string) => {
      const res = await api.get<TournamentSummary[] | TournamentsResponse>(path);
      const list = Array.isArray(res) ? res : (res?.tournaments ?? []);
      return { tournaments: list };
    },
    { refreshInterval: 8_000 },
  );

  const active = data?.tournaments?.find(
    (t) => String(t.status).toUpperCase() !== 'ENDED',
  );

  // Auto-redirect to active tournament
  useEffect(() => {
    if (active) {
      router.replace(`/arena/${active.id}`);
    }
  }, [active, router]);

  if (isLoading) {
    return (
      <div style={styles.center}>
        <Spinner size={28} />
        <span style={styles.hint}>{t('searching')}</span>
      </div>
    );
  }

  if (error) {
    return (
      <div style={styles.center}>
        <span style={{ ...styles.hint, color: color.bad }}>
          {t('error')}
        </span>
        <span style={{ ...styles.sub, marginTop: 4 }}>
          {(error as Error)?.message ?? t('retry')}
        </span>
      </div>
    );
  }

  if (!active) {
    return (
      <div style={styles.center}>
        <svg
          width="56" height="56" viewBox="0 0 56 56" fill="none"
          style={{ marginBottom: 20, opacity: 0.35 }}
        >
          {/* trophy icon */}
          <path
            d="M14 8h28M18 8v10a10 10 0 0020 0V8M14 8H8a6 6 0 006 6M42 8h6a6 6 0 01-6 6M28 38v8M20 46h16"
            stroke={color.txDim}
            strokeWidth="2.5"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
        <h2 style={styles.heading}>{t('no_active')}</h2>
        <p style={styles.sub}>
          {t('no_active_body_1')}
          <br />
          {t('no_active_body_2')}
        </p>
        {data?.tournaments && data.tournaments.length > 0 && (
          <div style={styles.list}>
            <span style={styles.listLabel}>{t('recent')}</span>
            {data.tournaments.slice(0, 5).map((t) => (
              <div key={t.id} style={styles.listItem}>
                <span style={styles.listName}>{t.name}</span>
                <span style={{ ...styles.listBadge, color: color.txDim }}>
                  {t.status}
                </span>
              </div>
            ))}
          </div>
        )}
      </div>
    );
  }

  // Redirecting (active found) — show spinner while router navigates
  return (
    <div style={styles.center}>
      <Spinner size={28} />
      <span style={styles.hint}>{t('entering')}</span>
    </div>
  );
}

// ── Local styles (CSS-in-JS) ──────────────────────────────────

const styles = {
  center: {
    display:        'flex',
    flexDirection:  'column' as const,
    alignItems:     'center',
    justifyContent: 'center',
    minHeight:      'calc(100vh - 56px)',
    gap:            12,
    padding:        '48px 24px',
    textAlign:      'center' as const,
  },
  heading: {
    margin:     0,
    fontSize:   22,
    fontWeight: 600,
    color:      color.txHi,
    fontFamily: 'var(--sans)',
  },
  hint: {
    fontSize:   15,
    color:      color.txDim,
    fontFamily: 'var(--sans)',
    marginTop:  8,
  },
  sub: {
    fontSize:   13,
    color:      color.txFaint,
    fontFamily: 'var(--sans)',
    lineHeight: 1.6,
  },
  list: {
    marginTop:      28,
    display:        'flex',
    flexDirection:  'column' as const,
    gap:            6,
    width:          '100%',
    maxWidth:       360,
    alignItems:     'stretch' as const,
  },
  listLabel: {
    fontSize:      11,
    letterSpacing: '0.08em',
    textTransform: 'uppercase' as const,
    color:         color.txFaint,
    marginBottom:  4,
    textAlign:     'left' as const,
  },
  listItem: {
    display:         'flex',
    justifyContent:  'space-between',
    alignItems:      'center',
    padding:         '8px 12px',
    background:      color.bgPanel,
    borderRadius:    7,
    border:          `1px solid ${color.line}`,
  },
  listName: {
    fontSize:   13,
    color:      color.tx,
    fontFamily: 'var(--sans)',
  },
  listBadge: {
    fontSize:      11,
    letterSpacing: '0.06em',
    textTransform: 'uppercase' as const,
    fontFamily:    'var(--mono)',
  },
} as const;
