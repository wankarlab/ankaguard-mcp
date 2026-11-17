'use client';

// ActiveAlertsBar — the in-app view of the basket-health alerts the backend
// monitor also pushes to the owner's phone. Data is the polled /xsec/status
// `active_alerts` (written to xsec_alert_state by the XSecAlertMonitor), so it
// matches the codebase's SWR-polling model rather than the auth-gated WebSocket
// hub. Renders nothing when there are no active alerts.

import { useTranslations } from 'next-intl';
import useSWR from 'swr';
import { getXsecStatus, type XSecStatus } from '@/lib/xsec-api';

function fmtSince(iso: string): string {
  const d = new Date(iso);
  if (isNaN(d.getTime()) || d.getUTCFullYear() < 2000) return '';
  return d.toLocaleString();
}

export function ActiveAlertsBar() {
  const t = useTranslations('Xsec');
  const { data } = useSWR<XSecStatus>('xsec:status', getXsecStatus, {
    revalidateOnFocus: false,
    refreshInterval: 15_000,
  });

  const alerts = data?.active_alerts ?? [];
  if (alerts.length === 0) return null;

  return (
    <div style={{ marginBottom: 16 }}>
      <div className="mono" style={{ fontSize: 10, letterSpacing: '.06em', color: 'var(--down)', textTransform: 'uppercase', marginBottom: 6 }}>
        {t('active_alerts_title')} ({alerts.length})
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
        {alerts.map((a) => (
          <div
            key={a.key}
            style={{
              padding: '10px 14px', borderRadius: 'var(--r)',
              border: '1px solid var(--down)', background: 'rgba(233,90,90,.07)',
            }}
          >
            <div style={{ fontSize: 13, fontWeight: 700, color: 'var(--down)' }}>{a.title}</div>
            <div className="mono" style={{ fontSize: 11, color: 'var(--tx-dim)', marginTop: 2 }}>{a.body}</div>
            {a.fired_at && (
              <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', marginTop: 4 }}>
                {t('active_alerts_since')} {fmtSince(a.fired_at)}
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
