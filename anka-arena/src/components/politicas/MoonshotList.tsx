'use client';

import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { apiFetch } from '@/lib/api';

interface Moonshot {
  id: string;
  name: string;
  reason: string;
}

// Raw moonshot item as returned by the backend (all fields optional).
interface MoonshotPayload {
  id?: string;
  name?: string;
  reason?: string;
  description?: string;
  risk?: string;
}

// Backend: { data: { moonshots: [{id,name,description,...}] } } (unwrapped to {moonshots}).
const fetcher = async (path: string): Promise<Moonshot[]> => {
  const d = await apiFetch<MoonshotPayload[] | { moonshots?: MoonshotPayload[] } | null>(path);
  const list: MoonshotPayload[] = Array.isArray(d) ? d : (d?.moonshots ?? []);
  return list.map((m) => ({
    id:     m.id ?? '',
    name:   m.name ?? m.id ?? '—',
    reason: m.reason ?? m.description ?? m.risk ?? '',
  }));
};

export function MoonshotList() {
  const t = useTranslations('UiPolicies');
  const { data, error, isLoading } = useSWR<Moonshot[]>(
    '/api/v1/admin/moonshots',
    fetcher,
    { revalidateOnFocus: false },
  );

  if (isLoading) {
    return (
      <div style={{ padding: 14 }}>
        <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}>
          {t('loading_moonshots')}
        </div>
      </div>
    );
  }

  // Anti-hardcode rule: never fabricate moonshots. On error show the real state.
  if (error) {
    return (
      <div style={{ padding: 14 }}>
        <div className="mono" style={{ fontSize: 11, color: '#f0564f' }}>
          {t('moonshots_error')}
        </div>
      </div>
    );
  }

  const moonshots = data ?? [];

  if (moonshots.length === 0) {
    return (
      <div style={{ padding: 14 }}>
        <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}>
          {t('moonshots_empty')}
        </div>
      </div>
    );
  }

  return (
    <div style={{ padding: 14, display: 'flex', flexDirection: 'column', gap: 9 }}>
      {moonshots.map((m) => (
        <div
          key={m.id}
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            padding: '9px 12px',
            background: 'var(--bg-void)',
            border: '1px solid var(--line)',
            borderRadius: 8,
          }}
        >
          <div>
            <div style={{ fontSize: 11.5, fontWeight: 500, color: 'var(--tx-hi)' }}>
              {m.name}
            </div>
            <div style={{ fontSize: 10, color: 'var(--tx-faint)', marginTop: 2 }}>
              {m.reason}
            </div>
          </div>

          <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexShrink: 0 }}>
            {/* Moon icon */}
            <svg
              width="14"
              height="14"
              viewBox="0 0 24 24"
              fill="none"
              stroke="var(--tx-faint)"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
            >
              <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" />
            </svg>

            {/* OFF toggle — disabled, non-interactive */}
            <div
              aria-label={t('moonshot_disabled', { name: m.name })}
              aria-disabled="true"
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 6,
                cursor: 'not-allowed',
                opacity: 0.7,
              }}
            >
              {/* Toggle pill */}
              <div
                style={{
                  width: 32,
                  height: 18,
                  borderRadius: 9,
                  background: 'var(--bg-card)',
                  border: '1px solid var(--line)',
                  position: 'relative',
                  flexShrink: 0,
                }}
              >
                <div
                  style={{
                    position: 'absolute',
                    top: 2,
                    left: 2,
                    width: 12,
                    height: 12,
                    borderRadius: '50%',
                    background: 'var(--tx-faint)',
                  }}
                />
              </div>

              <span
                style={{
                  fontSize: 8.5,
                  padding: '2px 6px',
                  borderRadius: 4,
                  background: 'var(--bg-card)',
                  color: 'var(--tx-faint)',
                  fontFamily: 'var(--mono)',
                  fontWeight: 600,
                  letterSpacing: '.05em',
                  border: '1px solid var(--line)',
                }}
              >
                OFF
              </span>
            </div>
          </div>
        </div>
      ))}

      <div
        className="mono"
        style={{ fontSize: 9.5, color: 'var(--tx-faint)', marginTop: 4 }}
      >
        {t('moonshots_footer')}
      </div>
    </div>
  );
}
