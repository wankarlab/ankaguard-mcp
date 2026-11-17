'use client';

import { useState } from 'react';
import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { api, apiFetch } from '@/lib/api';
import { usePermissionsStore } from '@/stores/permissions';

interface PolicyValues {
  min_pf_oos: number;
  min_green_pct: number;
  max_drift: number;
}

// Audit-trail entry as returned by the backend PUT (snake_case from api.put —
// apiFetch only unwraps the envelope, it does NOT camelCase raw helpers).
interface PolicyRecord {
  pf_min_oos?: number;
  green_pct_min?: number;
  drift_max?: number;
  signed_by?: string;
  signed_at?: string;
}

// Screen slug as known by the backend permissions.locks[].screen (C1.4).
const SCREEN = 'politicas';

// Backend uses {pf_min_oos, green_pct_min, drift_max}; FE uses the inverse names.
interface PolicyPayload {
  min_pf_oos?: number;
  pf_min_oos?: number;
  min_green_pct?: number;
  green_pct_min?: number;
  max_drift?: number;
  drift_max?: number;
}

const fetcher = async (path: string): Promise<PolicyValues> => {
  const d = await apiFetch<PolicyPayload | null>(path);
  return {
    min_pf_oos:    d?.min_pf_oos    ?? d?.pf_min_oos    ?? 1.3,
    min_green_pct: d?.min_green_pct ?? d?.green_pct_min ?? 60,
    max_drift:     d?.max_drift     ?? d?.drift_max     ?? 0.15,
  };
};

export function PolicySliders() {
  const t = useTranslations('UiPolicies');
  const tc = useTranslations('Confirm');
  // C1.4 — the write gate is the BACKEND truth, not a client-side guess.
  // usePermissionsStore mirrors GET /api/v1/me/permissions (rbac.level.* config).
  // isLocked(politicas)=false means the caller's level >= required level.
  const lock      = usePermissionsStore((s) => s.lockFor(SCREEN));
  const loaded    = usePermissionsStore((s) => s.loaded);
  const permError = usePermissionsStore((s) => s.error);
  // Fail CLOSED: only writable when permissions loaded WITHOUT error and the
  // backend explicitly reports this screen's lock as unlocked. A missing lock
  // entry or a load error keeps the write disabled (avoids enabling Save and
  // then getting a 403 from the backend).
  const canWrite  = loaded && !permError && lock != null && !lock.locked;

  const { data, error, isLoading } = useSWR<PolicyValues>(
    '/api/v1/admin/policies',
    fetcher,
    { revalidateOnFocus: false },
  );

  const [pf, setPf] = useState<number | null>(null);
  const [green, setGreen] = useState<number | null>(null);
  const [drift, setDrift] = useState<number | null>(null);
  const [dirty, setDirty] = useState(false);

  // C1.6 — sign-off ceremony state.
  const [ceremonyOpen, setCeremonyOpen] = useState(false);
  const [reason, setReason] = useState('');
  const [confirmed, setConfirmed] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState('');
  const [audit, setAudit] = useState<PolicyRecord | null>(null);

  // Effective values: local state overrides remote data
  const effectivePf = pf ?? data?.min_pf_oos ?? 1.3;
  const effectiveGreen = green ?? data?.min_green_pct ?? 60;
  const effectiveDrift = drift ?? data?.max_drift ?? 0.15;

  function change<T extends number>(setter: (v: T) => void) {
    return (v: T) => {
      setter(v);
      setDirty(true);
      setCeremonyOpen(false);
      setSubmitError('');
      setAudit(null);
    };
  }

  function openCeremony() {
    setCeremonyOpen(true);
    setReason('');
    setConfirmed(false);
    setSubmitError('');
  }

  const reasonOk = reason.trim().length >= 10;

  // C1.6 — a policy change requires a mandatory reason + explicit confirmation
  // before the PUT is allowed. The backend records the signer (JWT) and writes
  // its own audit_log row; the response is the signed record we surface below.
  async function signOff() {
    if (!reasonOk || !confirmed) return;
    setSubmitting(true);
    setSubmitError('');
    try {
      // Backend binds {pf_min_oos, green_pct_min, drift_max}. We also send the
      // reason so it travels with the request (backend ignores unknown fields
      // today; the ceremony makes the reason mandatory on the FE regardless).
      const rec = await api.put<PolicyRecord>('/api/v1/admin/policies', {
        pf_min_oos: effectivePf,
        green_pct_min: effectiveGreen,
        drift_max: effectiveDrift,
        reason: reason.trim(),
      });
      // C1.6 — show the change as an audit-trail entry read from the backend
      // (the returned signed record: who signed + when).
      setAudit(rec ?? null);
      setDirty(false);
      setCeremonyOpen(false);
    } catch {
      setSubmitError(t('sign_error'));
    } finally {
      setSubmitting(false);
    }
  }

  if (isLoading) {
    return (
      <div style={{ padding: 16 }}>
        <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}>
          {t('loading_thresholds')}
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <div style={{ padding: 16 }}>
        <div className="mono" style={{ fontSize: 11, color: '#f0564f' }}>
          {t('policies_error')}
        </div>
      </div>
    );
  }

  // ── Read-only mode (no write permission) ─────────────────────────────────────
  if (!canWrite) {
    return (
      <div style={{ padding: 16 }}>
        <div
          style={{
            display: 'flex',
            gap: 8,
            padding: '10px 12px',
            background: 'var(--bg-void)',
            borderRadius: 8,
            fontSize: 11,
            color: 'var(--tx-faint)',
          }}
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" style={{ flexShrink: 0, marginTop: 1 }}>
            <rect x="3" y="11" width="18" height="11" rx="2" ry="2" />
            <path d="M7 11V7a5 5 0 0 1 10 0v4" />
          </svg>
          <span>
            {t('read_only')}{' '}
            {lock?.howToUnlock || t('need_higher_level')}
          </span>
        </div>
        {/* Read-only view */}
        <div style={{ marginTop: 12, opacity: 0.6, pointerEvents: 'none' }}>
          <SliderRow
            label={t('min_pf_oos')}
            value={effectivePf}
            min={1.0} max={2.0} step={0.05}
            unit=""
            note="validator.go · MinProfitFactor"
            onChange={() => {}}
            disabled
          />
          <SliderRow
            label={t('min_green_pct')}
            value={effectiveGreen}
            min={40} max={90} step={5}
            unit="%"
            note="validation_service.go · MinConsistency"
            onChange={() => {}}
            disabled
          />
          <SliderRow
            label={t('max_drift')}
            value={effectiveDrift}
            min={0.05} max={0.40} step={0.01}
            unit=""
            note={t('drift_note')}
            onChange={() => {}}
            disabled
          />
        </div>
      </div>
    );
  }

  // ── Write mode (permission granted by backend) ───────────────────────────────
  return (
    <div style={{ padding: 16 }}>
      <SliderRow
        label={t('min_pf_oos')}
        value={effectivePf}
        min={1.0} max={2.0} step={0.05}
        unit=""
        note="validator.go · MinProfitFactor"
        onChange={change(setPf)}
      />
      <SliderRow
        label={t('min_green_pct')}
        value={effectiveGreen}
        min={40} max={90} step={5}
        unit="%"
        note="validation_service.go · MinConsistency"
        onChange={change(setGreen)}
      />
      <SliderRow
        label={t('max_drift')}
        value={effectiveDrift}
        min={0.05} max={0.40} step={0.01}
        unit=""
        note={t('drift_note')}
        onChange={change(setDrift)}
      />

      <div style={{ borderTop: '1px solid var(--line)', margin: '4px 0 14px' }} />

      {/* Audit-trail entry (C1.6) — read from the backend after a signed change */}
      {audit && (
        <div
          style={{
            padding: '10px 12px',
            background: 'rgba(74,219,132,.07)',
            border: '1px solid rgba(74,219,132,.3)',
            borderRadius: 8,
            marginBottom: 12,
            fontSize: 11,
            color: 'var(--tx)',
          }}
        >
          <div style={{ display: 'flex', alignItems: 'center', gap: 7, marginBottom: 6 }}>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="#4adb84" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0 }}>
              <path d="M20 6 9 17l-5-5" />
            </svg>
            <strong style={{ color: 'var(--tx-hi)' }}>{t('signed_change')}</strong>
          </div>
          <div className="mono" style={{ fontSize: 10.5, color: 'var(--tx-faint)', lineHeight: 1.6 }}>
            <div>{t('signed_by')} {audit.signed_by || '—'}</div>
            <div>{t('date')} {formatTs(audit.signed_at)}</div>
            <div>
              pf_min_oos {fmt(audit.pf_min_oos)} · green_pct_min {fmt(audit.green_pct_min)} ·
              drift_max {fmt(audit.drift_max)}
            </div>
          </div>
        </div>
      )}

      {/* Dirty banner */}
      {dirty && !ceremonyOpen && (
        <div
          style={{
            display: 'flex',
            gap: 8,
            padding: '10px 12px',
            background: 'rgba(233,162,62,.08)',
            border: '1px solid rgba(233,162,62,.3)',
            borderRadius: 8,
            marginBottom: 12,
            fontSize: 11,
            color: 'var(--tx)',
          }}
        >
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="#e9a23e" strokeWidth="2" style={{ flexShrink: 0, marginTop: 1 }}>
            <rect x="3" y="11" width="18" height="11" rx="2" ry="2" />
            <path d="M7 11V7a5 5 0 0 1 10 0v4" />
          </svg>
          <span>
            {t('dirty_1')}{' '}
            <strong>{t('dirty_strong')}</strong> {t('dirty_2')}
          </span>
        </div>
      )}

      {/* Sign-off ceremony (C1.6) — mandatory reason + explicit confirmation */}
      {ceremonyOpen && (
        <div
          style={{
            padding: '12px 14px',
            background: 'var(--bg-void)',
            border: '1px solid rgba(233,162,62,.4)',
            borderRadius: 8,
            marginBottom: 12,
          }}
        >
          <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--tx-hi)', marginBottom: 8 }}>
            {t('ceremony_title')}
          </div>

          <label style={{ fontSize: 11, color: 'var(--tx)', display: 'block', marginBottom: 5 }}>
            {t('reason_label')} <span style={{ color: '#e9a23e' }}>*</span>
          </label>
          <textarea
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            rows={3}
            placeholder={t('reason_placeholder')}
            style={{
              width: '100%',
              resize: 'vertical',
              fontSize: 12,
              fontFamily: 'var(--sans)',
              padding: '8px 10px',
              borderRadius: 6,
              border: '1px solid var(--line)',
              background: 'var(--bg-card)',
              color: 'var(--tx-hi)',
              boxSizing: 'border-box',
            }}
            aria-label={t('reason_aria')}
          />
          <div className="mono" style={{ fontSize: 9.5, color: reasonOk ? 'var(--tx-faint)' : '#e9a23e', marginTop: 3, marginBottom: 10 }}>
            {t('min_chars', { count: reason.trim().length })}
          </div>

          <label style={{ display: 'flex', gap: 8, alignItems: 'flex-start', fontSize: 11, color: 'var(--tx)', cursor: 'pointer', marginBottom: 12 }}>
            <input
              type="checkbox"
              checked={confirmed}
              onChange={(e) => setConfirmed(e.target.checked)}
              style={{ marginTop: 2, accentColor: 'var(--brand)', flexShrink: 0 }}
            />
            <span>
              {t('confirm_ownership')}
            </span>
          </label>

          {submitError && (
            <div
              style={{
                padding: '8px 12px',
                background: 'rgba(240,86,79,.08)',
                border: '1px solid rgba(240,86,79,.3)',
                borderRadius: 8,
                marginBottom: 10,
                fontSize: 11,
                color: '#f0564f',
              }}
            >
              {submitError}
            </div>
          )}

          <div style={{ display: 'flex', gap: 8 }}>
            <button
              className="btn"
              style={{ flex: 1, background: reasonOk && confirmed ? 'var(--brand)' : undefined }}
              disabled={!reasonOk || !confirmed || submitting}
              onClick={signOff}
            >
              {submitting ? t('signing') : t('sign_and_record')}
            </button>
            <button
              className="btn"
              style={{ flex: '0 0 auto' }}
              disabled={submitting}
              onClick={() => setCeremonyOpen(false)}
            >
              {tc('cancel')}
            </button>
          </div>
        </div>
      )}

      {!ceremonyOpen && (
        <button
          className="btn"
          style={{ width: '100%', background: dirty ? 'var(--brand)' : undefined }}
          disabled={!dirty}
          onClick={openCeremony}
        >
          {dirty ? t('start_ceremony') : t('no_pending_changes')}
        </button>
      )}
    </div>
  );
}

function fmt(v: number | undefined): string {
  return v == null ? '—' : String(v);
}

function formatTs(ts: string | undefined): string {
  if (!ts) return '—';
  const d = new Date(ts);
  return isNaN(d.getTime()) ? ts : d.toLocaleString();
}

// ─── Slider row ───────────────────────────────────────────────────────────────

function SliderRow({
  label,
  value,
  min,
  max,
  step,
  unit,
  note,
  onChange,
  disabled = false,
}: {
  label: string;
  value: number;
  min: number;
  max: number;
  step: number;
  unit: string;
  note: string;
  onChange: (v: number) => void;
  disabled?: boolean;
}) {
  const display =
    step < 0.1
      ? value.toFixed(2)
      : step < 1
      ? value.toFixed(2)
      : String(value);

  return (
    <div style={{ marginBottom: 16 }}>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          marginBottom: 6,
        }}
      >
        <span style={{ fontSize: 12, color: 'var(--tx)' }}>{label}</span>
        <span
          className="mono"
          style={{ fontSize: 13, fontWeight: 600, color: 'var(--tx-hi)' }}
        >
          {display}{unit}
        </span>
      </div>
      <input
        type="range"
        min={min}
        max={max}
        step={step}
        value={value}
        disabled={disabled}
        onChange={(e) => onChange(Number(e.target.value))}
        style={{ width: '100%', accentColor: 'var(--brand)' }}
        aria-label={label}
      />
      <div className="mono" style={{ fontSize: 9.5, color: 'var(--tx-faint)', marginTop: 3 }}>
        {note}
      </div>
    </div>
  );
}
