'use client';

import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { apiFetch } from '@/lib/api';
import { Meter } from '@/components/ui/Meter';

// A single robustness-score component, read entirely from the backend.
// Keys, labels and weights ALL come from GET /api/v1/admin/policies/robustness —
// nothing is hardcoded here (anti-hardcode rule: no business values in client).
interface WeightRow {
  key: string;
  label: string;
  pct: number; // 0–100 for the meter
}

// Backend: { data: { weights: { <key>: <number 0..1 or 0..100> }, version, note } }.
interface RobustnessPayload {
  weights?: Record<string, unknown>;
  version?: string;
  note?: string;
}

const fetcher = async (path: string): Promise<WeightRow[]> => {
  const d = await apiFetch<RobustnessPayload | null>(path);
  const raw: Record<string, unknown> = d?.weights ?? (d as Record<string, unknown> | null) ?? {};
  return Object.entries(raw)
    .filter(([, v]) => typeof v === 'number')
    .map(([key, v]) => {
      const n = v as number;
      // Backend may express weights as fractions (0..1) or percents (0..100).
      const pct = n <= 1 ? n * 100 : n;
      return {
        key,
        label: key.replace(/_/g, ' '),
        pct: Math.round(pct * 10) / 10,
      };
    });
};

export function RobustnessWeights() {
  const t = useTranslations('UiPolicies');
  const { data, error, isLoading } = useSWR<WeightRow[]>(
    '/api/v1/admin/policies/robustness',
    fetcher,
    { revalidateOnFocus: false },
  );

  return (
    <div style={{ padding: 16 }}>
      {isLoading && (
        <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)', marginBottom: 12 }}>
          {t('loading_weights')}
        </div>
      )}
      {error && (
        <div
          className="mono"
          style={{ fontSize: 11, color: '#f0564f', marginBottom: 12 }}
        >
          {t('weights_error')}
        </div>
      )}

      {!isLoading && !error && (data?.length ?? 0) === 0 && (
        <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)', marginBottom: 12 }}>
          {t('weights_empty')}
        </div>
      )}

      {(data ?? []).map(({ key, label, pct }) => (
        <div key={key} style={{ marginBottom: 12 }}>
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
              marginBottom: 5,
            }}
          >
            <span style={{ fontSize: 11.5, color: 'var(--tx)' }}>{label}</span>
            <span
              className="mono"
              style={{ fontSize: 13, fontWeight: 600, color: 'var(--tx-hi)' }}
            >
              {pct}%
            </span>
          </div>
          <Meter
            value={pct}
            color="var(--brand)"
            aria-label={`${label}: ${pct}%`}
          />
        </div>
      ))}

      <div
        className="mono"
        style={{ fontSize: 9.5, color: 'var(--tx-faint)', marginTop: 6 }}
      >
        {t('weights_footer')}
      </div>
    </div>
  );
}
