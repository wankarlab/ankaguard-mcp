'use client';

import useSWR, { mutate } from 'swr';
import { useState } from 'react';
import { useTranslations } from 'next-intl';
import { apiFetch, ApiError } from '@/lib/api';
import { useUIStore } from '@/stores/ui';
import { Panel, PanelHeader, PanelBody } from '@/components/ui/Panel';
import { Meter } from '@/components/ui/Meter';
import { Spinner } from '@/components/ui/Spinner';

// ── Types ─────────────────────────────────────────────────────────────────────

interface CourseStep {
  id:        string;
  title:     string;
  progress:  number;   // 0–100
  done:      boolean;
  unlocked:  boolean;
}

interface ProgressData {
  steps:        CourseStep[];
  canGraduate:  boolean;
  kycLevel:     number;
  nextStepId:   string | null;
}

// ── Step row ──────────────────────────────────────────────────────────────────

function StepRow({ step, isNext, onComplete }: {
  step:       CourseStep;
  isNext:     boolean;
  onComplete: (id: string) => Promise<void>;
}) {
  const t = useTranslations('PageAcademy');
  const [loading, setLoading] = useState(false);

  async function handleComplete() {
    setLoading(true);
    try {
      await onComplete(step.id);
    } finally {
      setLoading(false);
    }
  }

  return (
    <div style={{ marginBottom: 14 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 5 }}>
        <span style={{ fontSize: 12, color: step.done ? 'var(--up)' : step.unlocked ? 'var(--tx)' : 'var(--tx-faint)' }}>
          {step.done ? '✓ ' : step.unlocked ? '' : '○ '}
          {STAGE_IDS.includes(step.id) ? t(`stage_${step.id}` as 'stage_alumno') : step.title}
        </span>
        <span className="mono faint" style={{ fontSize: 10 }}>{step.progress}%</span>
      </div>

      <Meter
        value={step.progress}
        color={step.done ? 'var(--up)' : 'var(--brand)'}
        aria-label={t('progress_aria', { title: STAGE_IDS.includes(step.id) ? t(`stage_${step.id}` as 'stage_alumno') : step.title })}
      />

      {isNext && !step.done && (
        <button
          onClick={handleComplete}
          disabled={loading}
          style={{
            marginTop: 8,
            width: '100%',
            padding: '6px 0',
            borderRadius: 'var(--r-sm)',
            border: '1px solid var(--line-2)',
            background:  'var(--brand-soft)',
            color:       'var(--brand)',
            fontFamily:  'var(--mono)',
            fontSize:    10.5,
            fontWeight:  600,
            letterSpacing: '0.06em',
            textTransform: 'uppercase',
            cursor:      loading ? 'not-allowed' : 'pointer',
            opacity:     loading ? 0.6 : 1,
            transition:  'background 0.12s',
          }}
        >
          {loading ? '...' : t('mark_done')}
        </button>
      )}
    </div>
  );
}

// ── Main component ────────────────────────────────────────────────────────────

const PROGRESS_KEY = '/api/v1/academy/progress';

// The backend journey (current_step: 0=alumno,1=aprendiz,2=candidato,3=autor +
// completed_steps + can_graduate). The FE renders a 4-stage curriculum, so we
// synthesize the `steps` array from current_step when the backend doesn't send one.
// Titles are localized at render time (PageAcademy.stage_<id>).
const STAGE_IDS: string[] = ['alumno', 'aprendiz', 'candidato', 'autor'];

function toProgressData(raw: any): ProgressData {
  const d = raw ?? {};
  // Already in FE shape (future-proof): only trust it if `steps` is a real array.
  if (Array.isArray(d.steps)) {
    return {
      steps: (d.steps as CourseStep[]).filter((s) => s && typeof s.id === 'string'),
      canGraduate: d.canGraduate ?? d.can_graduate ?? false,
      kycLevel: d.kycLevel ?? 0,
      nextStepId: d.nextStepId ?? d.next_step ?? null,
    };
  }
  // Backend journey shape: synthesize the 4-stage curriculum from current_step.
  const current = Number(d.current_step ?? 0);
  const steps: CourseStep[] = STAGE_IDS.map((id, i) => ({
    id,
    title:    id,
    progress: i < current ? 100 : i === current ? 50 : 0,
    done:     i < current,
    unlocked: i <= current,
  }));
  // The "next step" is the first non-done stage; backend may also name it explicitly.
  const nextStepId =
    (typeof d.next_step === 'string' && d.next_step) ||
    steps.find((s) => !s.done)?.id ||
    null;
  return {
    steps,
    canGraduate: !!d.can_graduate,
    kycLevel:    0, // overridden in the component from the JWT claims
    nextStepId,
  };
}

const fetcher = async (url: string) => toProgressData(await apiFetch<unknown>(url));

export function CourseProgress() {
  const t = useTranslations('PageAcademy');
  const { data, error, isLoading } = useSWR<ProgressData>(PROGRESS_KEY, fetcher, {
    refreshInterval: 0,
    // Retry transient failures; surface 404/401 as the honest empty/error state.
    onErrorRetry: (err, _key, _cfg, revalidate, { retryCount }) => {
      if (err?.status === 404 || err?.status === 401) return;      // no retry on 404/401
      if (retryCount >= 2) return;
      setTimeout(() => revalidate({ retryCount }), 5000);
    },
  });

  const kycLevel = useUIStore((s) => s.kycLevel);

  // Only render real backend data — NO mock fallback. kycLevel comes from the
  // JWT claims (store), not the academy endpoint.
  const progress = data ? { ...data, kycLevel } : null;

  async function handleComplete(stepId: string) {
    try {
      await apiFetch(`/api/v1/academy/courses/${stepId}/complete`, { method: 'POST' });
    } catch (err) {
      // 403 ALREADY_COMPLETED (or any error): don't crash — just resync below.
      if (!(err instanceof ApiError) || err.status !== 403) {
        console.warn('course completion failed:', err);
      }
    } finally {
      // Re-fetch so the UI reflects the real backend state regardless.
      mutate(PROGRESS_KEY);
    }
  }

  return (
    <Panel>
      <PanelHeader title={t('route_title')} sub={t('route_sub')} />
      <PanelBody>
        {isLoading && !progress && (
          <div style={{ display: 'flex', justifyContent: 'center', padding: 16 }}>
            <Spinner size={18} />
          </div>
        )}

        {!isLoading && !progress && error && (
          <div style={{ fontSize: 11, color: 'var(--tx-dim)', padding: '8px 0' }}>
            {t('route_load_failed')}
          </div>
        )}

        {progress && (progress.steps?.length ?? 0) === 0 && (
          <div style={{ fontSize: 11, color: 'var(--tx-dim)', padding: '8px 0' }}>
            {t('route_none')}
          </div>
        )}

        {progress && (progress.steps?.length ?? 0) > 0 && (
          <>
            {(progress.steps ?? []).map((step) => (
              <StepRow
                key={step.id}
                step={step}
                isNext={step.id === progress.nextStepId}
                onComplete={handleComplete}
              />
            ))}

            {/* Graduate button */}
            <div style={{ marginTop: 6 }}>
              <button
                disabled={!progress.canGraduate || progress.kycLevel < 2}
                style={{
                  width:       '100%',
                  padding:     '8px 0',
                  borderRadius: 'var(--r)',
                  border:      '1px solid var(--line-2)',
                  background:  progress.canGraduate && progress.kycLevel >= 2
                    ? 'var(--brand-soft)'
                    : 'var(--bg-inset)',
                  color:       progress.canGraduate && progress.kycLevel >= 2
                    ? 'var(--brand)'
                    : 'var(--tx-faint)',
                  fontFamily:  'var(--mono)',
                  fontSize:    10.5,
                  fontWeight:  600,
                  letterSpacing: '0.06em',
                  textTransform: 'uppercase',
                  cursor:      progress.canGraduate && progress.kycLevel >= 2 ? 'pointer' : 'not-allowed',
                  display:     'flex',
                  alignItems:  'center',
                  justifyContent: 'center',
                  gap:         6,
                }}
              >
                <span>🔒</span> {t('graduate')}
              </button>

              <p className="dim" style={{ fontSize: 10, textAlign: 'center', marginTop: 7, marginBottom: 0 }}>
                {progress.canGraduate && progress.kycLevel < 2
                  ? t('kyc_required')
                  : t('complete_route')}
              </p>
            </div>
          </>
        )}
      </PanelBody>
    </Panel>
  );
}
