'use client';

// Logbook: recent persisted flywheel events plus live stream events when the
// WebSocket is connected (same stream the Flywheel screen uses).

import { useMemo } from 'react';
import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { getFlywheelEvents, type FlywheelEventsResponse } from '@/lib/home-api';
import { mergeLogbook } from '@/lib/home-logic';
import { useEngineStream } from '@/hooks/useEngineStream';
import { HomeBlock, BlockMessage } from './HomeBlock';

export function HomeLogbookBlock() {
  const t = useTranslations('Home');
  const { events: live, connected } = useEngineStream();
  const { data, error, isLoading } = useSWR<FlywheelEventsResponse>(
    'home:flywheel-events',
    () => getFlywheelEvents(20),
    { revalidateOnFocus: false },
  );

  const entries = useMemo(
    () => mergeLogbook(data?.events, connected ? live : [], 20),
    [data, live, connected],
  );

  return (
    <HomeBlock title={t('logbook_title')}>
      {isLoading && !data && <BlockMessage text={t('loading')} />}
      {error && !data && entries.length === 0 && <BlockMessage text={t('unavailable')} tone="error" />}
      {(data || entries.length > 0) && entries.length === 0 && <BlockMessage text={t('logbook_empty')} />}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
        {entries.map((e) => (
          <div key={e.id} style={{ display: 'flex', gap: 10, fontSize: 12 }}>
            <span className="mono" style={{ flex: 'none', minWidth: 120, fontSize: 10.5, color: 'var(--tx-faint)', marginTop: 1 }}>
              {new Date(e.ts).toLocaleString()}
            </span>
            <span style={{ color: 'var(--tx)' }}>{e.text}</span>
          </div>
        ))}
      </div>
      <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', marginTop: 8 }}>
        {connected ? t('logbook_live') : t('logbook_offline')}
      </div>
    </HomeBlock>
  );
}
