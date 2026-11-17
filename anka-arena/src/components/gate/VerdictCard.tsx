'use client';

// VerdictCard.tsx — Veredicto final: CERTIFIED o REJECTED
// Acciones reales: Promover a Shadow (POST /specs/{id}/promote) /
// Retirar (POST /specs/{id}/demote — el endpoint real, reemplaza el fantasma
// el fantasma /archive). Conflicto de interés (C1.5): el autor NO puede aprobar su propia
// spec; el botón se deshabilita con razón visible y el backend lo refuerza con
// 403 SELF_APPROVAL_FORBIDDEN.

import { useState } from 'react';
import { useTranslations } from 'next-intl';
import { ConfirmInline } from '@/components/ui/ConfirmInline';
import { promoteSpec, demoteSpec } from '@/lib/api';
import { ApiError } from '@/lib/api';
import { getCurrentUser } from '@/lib/auth';

// View-model for the Gate theater. Sourced entirely from the backend specs
// endpoint (no fabricated data). `windows` is the real walk-forward pass/fail pattern.
export interface GateSpec {
  id: string;
  author: string;
  authorId: string;
  userId: string;
  kind: 'humano' | 'IA' | string;
  regime: string;
  pf: number;
  cons: number;
  robust: number;
  windows: boolean[];
  verdict: 'certified' | 'rejected' | 'queued' | 'validating' | string;
  flashy?: boolean;
}

interface VerdictCardProps {
  spec: GateSpec;
  revealed: boolean;
}

export function VerdictCard({ spec, revealed }: VerdictCardProps) {
  const [promoting, setPromoting] = useState(false);
  const [promoted, setPromoted] = useState(false);
  const [archiving, setArchiving] = useState(false);
  const [archived, setArchived] = useState(false);
  const [confirmDemote, setConfirmDemote] = useState(false);
  const tc = useTranslations('Confirm');
  const t = useTranslations('UiGate');
  const [error, setError] = useState<string | null>(null);

  if (!revealed) {
    return (
      <div
        className="panel-inset"
        style={{ padding: 20, textAlign: 'center', color: 'var(--tx-faint)' }}
      >
        <div
          style={{
            fontSize: 28,
            color: 'var(--line-glow)',
            lineHeight: 1,
            marginBottom: 8,
          }}
        >
          ◈
        </div>
        <div className="mono" style={{ fontSize: 11, marginTop: 8 }}>
          {t('awaiting_run')}
        </div>
      </div>
    );
  }

  const ok = spec.verdict === 'certified';

  // C1.5 — Conflict of interest: the author cannot approve their own spec.
  const me = getCurrentUser();
  const myId = me?.user_id ?? '';
  const isOwnSpec =
    myId !== '' && (myId === spec.authorId || myId === spec.userId);

  async function handlePromote() {
    if (isOwnSpec) return;
    setPromoting(true);
    setError(null);
    try {
      await promoteSpec(spec.id);
      setPromoted(true);
    } catch (e) {
      if (e instanceof ApiError) {
        const body = e.body as { error?: { code?: string } } | undefined;
        if (body?.error?.code === 'SELF_APPROVAL_FORBIDDEN') {
          setError(t('err_self_approval'));
        } else {
          setError(t('err_promote_http', { status: e.status }));
        }
      } else {
        setError(t('err_promote_backend'));
      }
    } finally {
      setPromoting(false);
    }
  }

  async function handleArchive() {
    setConfirmDemote(false);
    setArchiving(true);
    setError(null);
    try {
      await demoteSpec(spec.id, 'retired from gate');
      setArchived(true);
    } catch (e) {
      if (e instanceof ApiError) {
        if (e.status === 409) {
          setError(t('err_only_live'));
        } else if (e.status === 404) {
          setError(t('err_not_found'));
        } else {
          setError(t('err_demote_http', { status: e.status }));
        }
      } else {
        setError(t('err_demote_backend'));
      }
    } finally {
      setArchiving(false);
    }
  }

  return (
    <div
      className="panel-inset enter"
      style={{
        padding: 18,
        borderColor: ok ? 'rgba(54,205,134,0.4)' : 'rgba(240,86,79,0.4)',
        background: ok ? 'var(--up-soft)' : 'var(--real-soft)',
      }}
    >
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          marginBottom: 10,
        }}
      >
        <span className="eyebrow">{t('verdict')}</span>
        {ok ? (
          <span className="badge ok">
            <span className="bd-dot" style={{ background: 'var(--up)' }} />
            CERTIFIED
          </span>
        ) : (
          <span className="badge real">
            <span className="bd-dot" style={{ background: 'var(--real)' }} />
            REJECTED
          </span>
        )}
      </div>

      <div
        className="hi"
        style={{
          fontSize: 20,
          fontWeight: 600,
          color: ok ? 'var(--up)' : 'var(--real)',
        }}
      >
        {ok ? t('edge_proven') : t('no_edge')}
      </div>

      {ok ? (
        <>
          <div className="dim" style={{ fontSize: 11.5, marginTop: 4 }}>
            {t('ttl')}
          </div>

          {/* C1.5 — visible reason when self-approval is blocked */}
          {isOwnSpec && (
            <div
              style={{
                marginTop: 10,
                padding: '8px 10px',
                borderRadius: 6,
                background: 'var(--real-soft)',
                border: '1px solid rgba(240,86,79,0.35)',
                fontSize: 11,
                color: 'var(--real)',
                lineHeight: 1.45,
              }}
            >
              {t('conflict_banner')}
            </div>
          )}

          <button
            className="btn primary"
            style={{ width: '100%', marginTop: 14 }}
            onClick={handlePromote}
            disabled={promoting || promoted || isOwnSpec}
            title={
              isOwnSpec
                ? t('err_self_approval')
                : undefined
            }
          >
            {promoted
              ? t('promoted')
              : promoting
                ? t('promoting')
                : t('promote_shadow')}
          </button>
        </>
      ) : (
        <>
          <div className="dim" style={{ fontSize: 11.5, marginTop: 4 }}>
            {spec.flashy
              ? t('flashy_reason')
              : t('below_threshold')}
          </div>
          <button
            className="btn ghost"
            style={{ width: '100%', marginTop: 14, color: 'var(--tx-dim)' }}
            onClick={() => setConfirmDemote(true)}
            disabled={archiving || archived || confirmDemote}
          >
            {archived
              ? t('retired')
              : archiving
                ? t('retiring')
                : t('retire_postmortem')}
          </button>
          {confirmDemote && !archived && (
            <div style={{ marginTop: 10 }}>
              <ConfirmInline
                message={tc('demote')}
                confirmLabel={tc('demote_btn')}
                cancelLabel={tc('cancel')}
                busy={archiving}
                onCancel={() => setConfirmDemote(false)}
                onConfirm={handleArchive}
              />
            </div>
          )}
        </>
      )}

      {error && (
        <div
          style={{
            marginTop: 10,
            fontSize: 11,
            color: 'var(--real)',
            lineHeight: 1.45,
          }}
        >
          {error}
        </div>
      )}
    </div>
  );
}
