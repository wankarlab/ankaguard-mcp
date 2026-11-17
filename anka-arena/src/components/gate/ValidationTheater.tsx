'use client';

// ValidationTheater.tsx — El corazón de la pantalla.
// Muestra el spec seleccionado y corre la validación walk-forward.
// Usa SSE si el backend lo soporta; sino simula reveal con setInterval(220ms).

import { useEffect, useRef, useState, useCallback } from 'react';
import { useTranslations } from 'next-intl';
import { WindowGrid } from './WindowGrid';
import { MetricsGrid } from './MetricsGrid';
import type { GateSpec } from './VerdictCard';

// Relative URL so Next.js proxy forwards to :8081 (see next.config.js).
const BASE_URL = (process.env.NEXT_PUBLIC_API_URL ?? '').replace(/\/$/, '');

interface ValidationTheaterProps {
  spec: GateSpec;
  /** Called when all windows are revealed — parent uses this to show VerdictCard */
  onValidate?: (spec: GateSpec) => void;
}

export function ValidationTheater({ spec, onValidate }: ValidationTheaterProps) {
  const t = useTranslations('UiGate');
  const [running, setRunning] = useState(false);
  const [shown, setShown] = useState<number>(spec.windows.length);
  // Live window results updated during simulation or SSE stream
  const [liveWindows, setLiveWindows] = useState<boolean[]>(spec.windows);

  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const esRef = useRef<EventSource | null>(null);

  // When spec changes, reset to fully-revealed state.
  useEffect(() => {
    setShown(spec.windows.length);
    setRunning(false);
    setLiveWindows(spec.windows);
    if (intervalRef.current) {
      clearInterval(intervalRef.current);
      intervalRef.current = null;
    }
    esRef.current?.close();
    esRef.current = null;
  }, [spec.id]); // eslint-disable-line react-hooks/exhaustive-deps

  // Simulated reveal (used when SSE is not available)
  const simulateFallback = useCallback(
    (windows: boolean[], notifyParent: () => void) => {
      let i = 0;
      setShown(0);
      setLiveWindows(windows);
      intervalRef.current = setInterval(() => {
        i++;
        setShown(i);
        if (i >= windows.length) {
          clearInterval(intervalRef.current!);
          intervalRef.current = null;
          setRunning(false);
          setTimeout(notifyParent, 350);
        }
      }, 220);
    },
    [],
  );

  const runValidation = useCallback(async () => {
    if (running) return;
    setRunning(true);
    setShown(0);
    setLiveWindows(spec.windows);

    const notifyParent = () => onValidate?.({ ...spec, windows: spec.windows });

    // 1. Fire POST to trigger server-side validation (fire-and-forget)
    try {
      await fetch(`${BASE_URL}/api/v1/specs/${spec.id}/validate`, {
        method: 'POST',
      });
    } catch {
      // backend may not be live — continue with simulation
    }

    // 2. Try SSE stream
    let sseResolved = false;

    try {
      const es = new EventSource(
        `${BASE_URL}/api/v1/specs/${spec.id}/validate/stream`,
      );
      esRef.current = es;

      const windowResults: boolean[] = [...spec.windows];

      es.addEventListener('window', (ev: MessageEvent) => {
        try {
          const data = JSON.parse(ev.data) as { index: number; pass: boolean };
          windowResults[data.index] = data.pass;
          setLiveWindows([...windowResults]);
          setShown(data.index + 1);
        } catch {
          // malformed event — ignore
        }
      });

      const onDone = () => {
        sseResolved = true;
        es.close();
        esRef.current = null;
        setRunning(false);
        setTimeout(() => {
          onValidate?.({ ...spec, windows: windowResults });
        }, 350);
      };

      es.addEventListener('done', onDone);

      es.onerror = () => {
        if (!sseResolved) {
          es.close();
          esRef.current = null;
          simulateFallback(spec.windows, notifyParent);
        }
      };

      // If no 'done' event arrives within 1.5 s, fall back to simulation
      const timeout = setTimeout(() => {
        if (!sseResolved && es.readyState !== EventSource.CLOSED) {
          es.close();
          esRef.current = null;
          simulateFallback(spec.windows, notifyParent);
        }
      }, 1500);

      es.addEventListener('done', () => clearTimeout(timeout));
    } catch {
      simulateFallback(spec.windows, notifyParent);
    }
  }, [running, spec, simulateFallback, onValidate]);

  const greenCount = liveWindows.filter(Boolean).length;

  return (
    <div className="panel" style={{ overflow: 'hidden' }}>
      {/* Header */}
      <div className="panel-h">
        <h3
          className="mono"
          style={{
            fontSize: 12,
            flex: 1,
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
          }}
        >
          {spec.id}
        </h3>
        <span className="sub">{t('immutable')}</span>
        <button
          className="btn primary sm"
          onClick={runValidation}
          disabled={running}
          style={{ marginLeft: 8, flexShrink: 0 }}
        >
          {running ? t('running') : t('run_validation')}
        </button>
      </div>

      {/* Body */}
      <div style={{ padding: 18 }}>
        <WindowGrid windows={liveWindows} revealed={shown} />

        <div style={{ marginTop: 18 }}>
          <MetricsGrid
            metrics={{
              pf: spec.pf,
              cons: spec.cons,
              robust: spec.robust,
              greenWindows: greenCount,
              totalWindows: liveWindows.length,
            }}
          />
        </div>
      </div>
    </div>
  );
}
