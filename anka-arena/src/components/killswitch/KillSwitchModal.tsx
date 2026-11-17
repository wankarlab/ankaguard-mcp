'use client';

import { useCallback, useEffect, useState } from 'react';
import { useTranslations } from 'next-intl';
import { useUIStore } from '@/stores/ui';
import { api, ApiError } from '@/lib/api';
import {
  RING0_CONFIRM_TEXT, apiErrorDetail, executeRing0, previewRing0, type Ring0Result,
} from '@/lib/killswitch-ring0';
import type { Phase, Ring, RingId } from './types';
import { RINGS } from './types';
import { isProductEdition } from '@/lib/productMode';
import { KillSwitchStatusPanel } from './KillSwitchStatusPanel';
import { ConcentricRings } from './ConcentricRings';
import { BiometricAuth } from './BiometricAuth';
import { Ring0Preview, Ring0ResultPanel } from './Ring0Panels';
import {
  Ring1Confirm,
  Ring2Confirm,
  Ring0Confirm,
  DonePanel,
  ErrorPanel,
} from './ConfirmPanels';

export function KillSwitchModal() {
  const t = useTranslations('KillSwitch');
  const isOpen = useUIStore((s) => s.killSwitchOpen);
  const closeKillSwitch = useUIStore((s) => s.closeKillSwitch);

  const [selectedId, setSelectedId] = useState<RingId | null>(null);
  const [phase, setPhase] = useState<Phase>('idle');

  // Per-ring input fields
  const [specId, setSpecId] = useState('');
  const [tenantId, setTenantId] = useState('');
  const [confirmText, setConfirmText] = useState('');

  const [loading, setLoading] = useState(false);
  const [errorMsg, setErrorMsg] = useState('');
  const [preview, setPreview] = useState<Ring0Result | null>(null);
  const [previewError, setPreviewError] = useState('');
  const [ring0Result, setRing0Result] = useState<Ring0Result | null>(null);

  // Product edition: one operator, one bot, one exchange. Only the global stop
  // applies (rings 1 and 2 are platform specs and tenants) and the simulated
  // biometric step adds nothing on a self-hosted console; the dry-run preview
  // and the typed CERRAR TODO confirmation stay.
  const product = isProductEdition();
  const ring0 = RINGS.find((r) => r.id === 0)!;

  const ring = selectedId !== null ? RINGS.find((r) => r.id === selectedId)! : null;

  const handleClose = useCallback(() => {
    closeKillSwitch();
    // Reset after animation
    setTimeout(() => {
      setSelectedId(null);
      setPhase('idle');
      setSpecId('');
      setTenantId('');
      setConfirmText('');
      setErrorMsg('');
      setPreview(null);
      setPreviewError('');
      setRing0Result(null);
      setLoading(false);
    }, 200);
  }, [closeKillSwitch]);

  // Close on Escape
  useEffect(() => {
    if (!isOpen) return;
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape') handleClose();
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [isOpen, handleClose]);

  function selectRing(r: Ring) {
    setSelectedId(r.id);
    setErrorMsg('');
    if (r.id === 0) {
      // Ring 0: show the dry-run preview first, then biometric + typed confirm.
      setPhase('preview');
      void loadPreview();
    } else if (r.friction === 'click') {
      setPhase('confirm1');
    } else if (r.friction === 'bio') {
      setPhase('bio');
    } else {
      // bio+type: bio first
      setPhase('bio');
    }
  }

  async function loadPreview() {
    setLoading(true);
    setPreview(null);
    setPreviewError('');
    try {
      setPreview(await previewRing0());
    } catch (err) {
      setPreviewError(describeError(err));
    } finally {
      setLoading(false);
    }
  }

  function describeError(err: unknown): string {
    if (err instanceof ApiError) {
      const detail = apiErrorDetail(err) || (typeof err.body === 'string' ? err.body : '');
      if (err.status === 401) return t('err_401');
      if (err.status === 403) return t('err_403');
      return `${t('err_http', { status: err.status })}${detail ? ` · ${detail}` : ''}`;
    }
    return t('unknown_exec_error');
  }

  function onBioDone() {
    if (!ring) return;
    if (ring.id === 0) {
      // After bio, go to type confirmation
      setPhase('type');
    } else {
      // Ring 2: after bio, confirm tenant
      setPhase('confirm1');
    }
  }

  async function executeRing() {
    if (!ring) return;

    // Client-side validation for ring 0
    if (ring.id === 0 && confirmText !== RING0_CONFIRM_TEXT) return;

    setLoading(true);
    try {
      if (ring.id === 0) {
        setRing0Result(await executeRing0(confirmText));
        setPhase('done');
        return;
      }
      const payload: Record<string, string> = {};
      if (ring.id === 1) payload.spec_id = specId;
      if (ring.id === 2) payload.tenant_id = tenantId;

      await api.post(ring.endpoint, payload);
      setPhase('done');
    } catch (err) {
      setErrorMsg(describeError(err));
      setPhase('error');
    } finally {
      setLoading(false);
    }
  }

  if (!isOpen) return null;

  return (
    <>
      <style>{`
        @keyframes ks-pulse {
          0%, 100% { box-shadow: 0 0 0 0 rgba(240,86,79,.4); }
          50%       { box-shadow: 0 0 0 10px rgba(240,86,79,0); }
        }
      `}</style>

      {/* Scrim */}
      <div
        className="modal-scrim"
        role="dialog"
        aria-modal="true"
        aria-label={t(product ? 'product_console_title' : 'console_title')}
        onClick={(e) => {
          if ((e.target as HTMLElement).classList.contains('modal-scrim')) handleClose();
        }}
        style={{
          position: 'fixed',
          inset: 0,
          zIndex: 9999,
          background: 'rgba(0,0,0,.65)',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          backdropFilter: 'blur(2px)',
        }}
      >
        <div
          className="modal"
          style={{
            width: product ? 460 : 640,
            maxWidth: '95vw',
            background: 'var(--bg-card)',
            border: '1px solid var(--line)',
            borderRadius: 12,
            overflow: 'hidden',
          }}
        >
          {/* Header */}
          <div
            className="drawer-h"
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 10,
              padding: '12px 16px',
              borderBottom: '1px solid rgba(240,86,79,.3)',
            }}
          >
            {/* Live pulse dot */}
            <span
              style={{
                width: 9,
                height: 9,
                borderRadius: '50%',
                background: '#f0564f',
                flexShrink: 0,
                animation: 'ks-pulse 1.8s ease-in-out infinite',
              }}
            />
            <div style={{ flex: 1 }}>
              <div style={{ fontWeight: 600, fontSize: 13, color: 'var(--tx-hi)' }}>
                {t(product ? 'product_console_title' : 'console_title')}
              </div>
              <div
                className="mono"
                style={{ fontSize: 10, color: 'var(--tx-faint)' }}
              >
                {t(product ? 'product_console_subtitle' : 'console_subtitle')}
              </div>
            </div>
            <button
              className="btn icon ghost"
              onClick={handleClose}
              aria-label={t('close')}
              style={{
                background: 'none',
                border: 'none',
                cursor: 'pointer',
                color: 'var(--tx-faint)',
                padding: 4,
                borderRadius: 6,
                lineHeight: 1,
              }}
            >
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
                <line x1="18" y1="6" x2="6" y2="18" />
                <line x1="6" y1="6" x2="18" y2="18" />
              </svg>
            </button>
          </div>

          {/* Body */}
          <div style={{ display: 'grid', gridTemplateColumns: product ? '1fr' : 'repeat(auto-fit, minmax(300px, 1fr))', gap: 0 }}>
            {/* Left: concentric rings SVG */}
            {!product && (<div
              style={{
                padding: 20,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                borderRight: '1px solid var(--line)',
              }}
            >
              <ConcentricRings selectedId={selectedId} onSelect={selectRing} />
            </div>)}

            {/* Right: control panel */}
            <div style={{ padding: 18, minHeight: product ? 0 : 340 }}>
              {phase === 'idle' && product && (
                <>
                  <div className="mono" style={{ fontSize: 11, color: ring0.color, fontWeight: 600, letterSpacing: '.08em' }}>
                    {t('product_stop_name')}
                  </div>
                  <p style={{ fontSize: 12.5, color: 'var(--tx)', margin: '6px 0 12px' }}>{t('product_stop_desc')}</p>
                  <button type="button" className="btn danger" style={{ width: '100%' }} onClick={() => selectRing(ring0)}>
                    {t('product_start_btn')}
                  </button>
                  <KillSwitchStatusPanel />
                  <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 14 }}>{t('product_global_note')}</div>
                </>
              )}
              {phase === 'idle' && !product && (
                <>
                  <div
                    className="eyebrow"
                    style={{ marginBottom: 10, fontSize: 10, letterSpacing: '.08em', color: 'var(--tx-faint)', textTransform: 'uppercase' }}
                  >
                    {t('choose_radius')}
                  </div>
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 9 }}>
                    {RINGS.map((r) => (
                      <div
                        key={r.id}
                        role="button"
                        tabIndex={0}
                        className="panel-inset"
                        style={{ padding: '11px 13px', cursor: 'pointer', borderColor: `${r.color}55`, borderRadius: 8 }}
                        onClick={() => selectRing(r)}
                        onKeyDown={(e) => e.key === 'Enter' && selectRing(r)}
                      >
                        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
                          <span
                            className="mono"
                            style={{ fontSize: 11, color: r.color, fontWeight: 600 }}
                          >
                            {t(`ring${r.id}_name`)}
                          </span>
                          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="var(--tx-faint)" strokeWidth="2" strokeLinecap="round">
                            <polyline points="9 18 15 12 9 6" />
                          </svg>
                        </div>
                        <div style={{ fontSize: 11.5, color: 'var(--tx)', marginTop: 4 }}>
                          {t(`ring${r.id}_desc`)}
                        </div>
                        <div className="mono" style={{ fontSize: 9.5, color: 'var(--tx-faint)', marginTop: 3 }}>
                          {t('friction')}: {r.friction}
                        </div>
                      </div>
                    ))}
                  </div>
                  <KillSwitchStatusPanel />
                  <div
                    style={{
                      display: 'flex',
                      gap: 8,
                      marginTop: 14,
                      padding: '10px 12px',
                      background: 'var(--bg-void)',
                      borderRadius: 8,
                      fontSize: 11,
                      color: 'var(--tx-faint)',
                    }}
                  >
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="#e9a23e" strokeWidth="2" strokeLinecap="round" style={{ flexShrink: 0, marginTop: 1 }}>
                      <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" />
                    </svg>
                    <span>
                      {t('global_note')}
                    </span>
                  </div>
                </>
              )}

              {/* Ring 0 — dry-run preview */}
              {phase === 'preview' && ring?.id === 0 && (
                <Ring0Preview
                  ring={ring}
                  preview={preview}
                  loading={loading}
                  error={previewError}
                  onContinue={() => setPhase(product ? 'type' : 'bio')}
                  onRetry={loadPreview}
                  onCancel={() => setPhase('idle')}
                />
              )}

              {/* Ring 1 — spec freeze */}
              {phase === 'confirm1' && ring?.id === 1 && (
                <Ring1Confirm
                  ring={ring}
                  specId={specId}
                  setSpecId={setSpecId}
                  onCancel={() => setPhase('idle')}
                  onConfirm={executeRing}
                  loading={loading}
                />
              )}

              {/* Ring 2 — tenant isolate (after bio) */}
              {phase === 'confirm1' && ring?.id === 2 && (
                <Ring2Confirm
                  ring={ring}
                  tenantId={tenantId}
                  setTenantId={setTenantId}
                  onCancel={() => setPhase('idle')}
                  onConfirm={executeRing}
                  loading={loading}
                />
              )}

              {/* Biometric auth (rings 0 and 2) */}
              {phase === 'bio' && ring && (
                <BiometricAuth
                  ring={ring}
                  onDone={onBioDone}
                  allowSkip={ring.id === 0}
                  onCancel={() => setPhase('idle')}
                />
              )}

              {/* Ring 0 — type confirmation (after bio) */}
              {phase === 'type' && ring?.id === 0 && (
                <Ring0Confirm
                  ring={ring}
                  confirm={confirmText}
                  setConfirm={setConfirmText}
                  onCancel={() => setPhase('idle')}
                  onConfirm={executeRing}
                  loading={loading}
                />
              )}

              {/* Done */}
              {phase === 'done' && ring?.id === 0 && ring0Result && (
                <Ring0ResultPanel ring={ring} result={ring0Result} onClose={handleClose} />
              )}
              {phase === 'done' && ring && ring.id !== 0 && (
                <DonePanel ring={ring} onClose={handleClose} />
              )}

              {/* Error */}
              {phase === 'error' && ring && (
                <ErrorPanel
                  ring={ring}
                  message={errorMsg}
                  onRetry={() => {
                    if (ring.id === 0) setPhase('type');
                    else if (ring.friction === 'bio') setPhase('bio');
                    else setPhase('confirm1');
                  }}
                  onCancel={() => setPhase('idle')}
                />
              )}
            </div>
          </div>
        </div>
      </div>
    </>
  );
}
