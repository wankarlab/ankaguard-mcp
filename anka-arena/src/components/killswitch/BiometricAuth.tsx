import { useEffect, useRef, useState } from 'react';
import { useTranslations } from 'next-intl';
import type { Ring } from './types';

export function BiometricAuth({
  ring,
  onDone,
  onCancel,
  allowSkip = false,
}: {
  ring: Ring;
  allowSkip?: boolean;
  onDone: () => void;
  onCancel: () => void;
}) {
  const t = useTranslations('KillSwitch');
  const [countdown, setCountdown] = useState(3);
  const [scanning, setScanning] = useState(false);
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null);
  // Always call the latest onDone; never invoke it from inside a state updater
  // (side effects there run twice in StrictMode and update the parent mid-render).
  const onDoneRef = useRef(onDone);
  onDoneRef.current = onDone;
  const remainingRef = useRef(3);

  // NOTE: this step is a UI friction gate (a timed "scan"), not real WebAuthn,
  // so it cannot fail for lack of a platform authenticator. The typed
  // confirmation (CERRAR TODO) is the actual safety; allowSkip offers a direct
  // path to it so this step can never block the global stop.
  const startScan = () => {
    if (scanning) return;
    setScanning(true);
    setCountdown(3);
    remainingRef.current = 3;
    intervalRef.current = setInterval(() => {
      remainingRef.current -= 1;
      setCountdown(Math.max(remainingRef.current, 0));
      if (remainingRef.current <= 0) {
        if (intervalRef.current) clearInterval(intervalRef.current);
        intervalRef.current = null;
        onDoneRef.current();
      }
    }, 1000);
  };

  useEffect(() => {
    return () => {
      if (intervalRef.current) clearInterval(intervalRef.current);
    };
  }, []);

  return (
    <div style={{ textAlign: 'center', paddingTop: 20 }}>
      <div
        className="mono"
        style={{ color: ring.color, fontSize: 11, letterSpacing: '.08em', marginBottom: 4 }}
      >
        {t(`ring${ring.id}_name`)}
      </div>
      <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginBottom: 20 }}>
        {t('bio_required')}
      </div>

      {/* Fingerprint circle */}
      <button
        onClick={startScan}
        disabled={scanning}
        aria-label={t('bio_start')}
        style={{
          width: 90,
          height: 90,
          margin: '0 auto 16px',
          borderRadius: '50%',
          border: `2px solid ${ring.color}`,
          background: 'var(--bg-card)',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          cursor: scanning ? 'default' : 'pointer',
          animation: scanning ? 'ks-pulse 1.2s ease-in-out infinite' : 'none',
          transition: 'box-shadow .2s',
          boxShadow: scanning ? `0 0 0 6px ${ring.color}22` : 'none',
        }}
      >
        {/* Fingerprint SVG icon */}
        <svg
          width="40"
          height="40"
          viewBox="0 0 24 24"
          fill="none"
          stroke={ring.color}
          strokeWidth="1.5"
          strokeLinecap="round"
          strokeLinejoin="round"
        >
          <path d="M12 10a2 2 0 0 0-2 2c0 1.02-.1 2.51-.26 4" />
          <path d="M14 13.12c0 2.38 0 6.38-1 8.88" />
          <path d="M17.29 21.02c.12-.6.43-2.3.5-3.02" />
          <path d="M2 12a10 10 0 0 1 18-6" />
          <path d="M2 17a10 10 0 0 0 9.9 2.99" />
          <path d="M8 10a4 4 0 0 1 8 0c0 1.5-.23 2.74-.5 4" />
          <path d="M2.5 8.875A10 10 0 0 0 2 12v.5" />
          <path d="M20 8.5c.23.986.5 1.807.5 3.5a6.24 6.24 0 0 1-.24 2" />
          <path d="M12 14a2 2 0 0 0 2-2" />
        </svg>
      </button>

      {scanning ? (
        <div className="mono" style={{ color: ring.color, fontSize: 13, fontWeight: 600 }}>
          {t('scanning', { seconds: countdown })}
        </div>
      ) : (
        <div style={{ fontSize: 12.5, color: 'var(--tx)' }}>
          {t('bio_tap')}
        </div>
      )}
      <div className="mono" style={{ fontSize: 9.5, color: 'var(--tx-faint)', marginTop: 6 }}>
        {t('friction_note')}
      </div>
      {allowSkip && (
        <div>
          <button className="btn ghost sm" style={{ marginTop: 12 }} onClick={onDone} disabled={scanning}>
            {t('bio_skip')}
          </button>
        </div>
      )}
      <button className="btn ghost sm" style={{ marginTop: 16 }} onClick={onCancel}>
        {t('cancel')}
      </button>
    </div>
  );
}
