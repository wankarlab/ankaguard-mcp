'use client';

import { useState } from 'react';
import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { fetchHistory, rollback, type HistoryRow } from '@/lib/scalping';

interface Props {
  canWrite: boolean;
}

function fmtTs(ts: string): string {
  try {
    return new Date(ts).toLocaleString();
  } catch {
    return ts;
  }
}

export function ChangeHistory({ canWrite }: Props) {
  const t = useTranslations('Scalping');
  const { data, error, isLoading, mutate } = useSWR(
    'scalping:history',
    () => fetchHistory(30),
    { revalidateOnFocus: false },
  );
  const [busyId, setBusyId] = useState<number | null>(null);

  async function doRollback(row: HistoryRow) {
    if (!canWrite || row.old_value == null) return;
    setBusyId(row.id);
    try {
      await rollback(row.id);
      await mutate();
    } finally {
      setBusyId(null);
    }
  }

  if (isLoading) {
    return <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{t('loading')}</div>;
  }
  if (error) {
    return <div className="mono" style={{ fontSize: 11, color: 'var(--down)' }}>{t('load_error')}</div>;
  }
  const rows = data ?? [];
  if (rows.length === 0) {
    return <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{t('history_empty')}</div>;
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
      {rows.map((row) => (
        <div
          key={row.id}
          style={{
            display: 'grid', gridTemplateColumns: '1fr auto', gap: 10,
            alignItems: 'center', padding: '7px 8px', borderRadius: 6,
            borderBottom: '1px solid var(--line)',
          }}
        >
          <div>
            <div className="mono" style={{ fontSize: 11, color: 'var(--tx-hi)' }}>
              {row.key}{' '}
              <span style={{ color: 'var(--tx-faint)' }}>
                {row.old_value ?? '∅'} → <span style={{ color: 'var(--brand)' }}>{row.new_value ?? '∅'}</span>
              </span>
            </div>
            <div className="mono" style={{ fontSize: 9, color: 'var(--tx-faint)' }}>
              {fmtTs(row.changed_at)} · {row.changed_by}
              {row.change_reason ? ` · ${row.change_reason}` : ''}
            </div>
          </div>
          {row.old_value != null && (
            <button
              type="button"
              disabled={!canWrite || busyId === row.id} suppressHydrationWarning
              onClick={() => doRollback(row)}
              title={t('rollback')}
              style={{
                padding: '4px 10px', borderRadius: 4,
                border: '1px solid var(--line)', background: 'transparent',
                color: canWrite ? 'var(--tx-dim)' : 'var(--tx-faint)',
                fontSize: 11, cursor: canWrite ? 'pointer' : 'not-allowed',
                whiteSpace: 'nowrap',
              }}
            >
              ↺ {busyId === row.id ? '…' : t('rollback')}
            </button>
          )}
        </div>
      ))}
    </div>
  );
}
