'use client';

// C3.1 — Approvals panel (operator): real queue of specs awaiting promote.
//
// Reads GET /api/v1/specs?status=shadow (specs validated and awaiting an
// operator's promote decision). Conflict-of-interest (C1.5 / rule #6) is
// codified in the UI: an operator cannot approve their OWN spec — the Approve
// button is pre-disabled when GateSpec.authorId === claims.sub. The backend
// also enforces this (403 SELF_APPROVAL_FORBIDDEN); we surface that honestly
// instead of discovering it via an error.

import { useState } from 'react';
import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { ConfirmInline } from '@/components/ui/ConfirmInline';
import { fetchSpecs, promoteSpec, ApiError } from '@/lib/api';
import { getCurrentUser } from '@/lib/auth';
import { LockedScreen } from '@/components/common/LockedScreen';
import { Panel, PanelHeader, PanelBody } from '@/components/ui/Panel';
import { Badge } from '@/components/ui/Badge';
import { Spinner } from '@/components/ui/Spinner';
import type { GateSpec } from '@/lib/adapters';

// Shared by /aprobaciones and the Gate theater.
const QUEUE_KEY = 'aprobaciones:shadow-queue';

export function ApprovalsPanel() {
  const t = useTranslations('PageApprovals');
  const { data: specs, error, isLoading, mutate } = useSWR<GateSpec[]>(
    QUEUE_KEY,
    () => fetchSpecs({ status: 'shadow' }),
    { refreshInterval: 20_000 },
  );

  const me = getCurrentUser();
  const myId = me?.user_id ?? '';

  // Per-spec action state.
  const [busyId, setBusyId] = useState<string | null>(null);
  const [feedback, setFeedback] = useState<Record<string, { ok: boolean; msg: string }>>({});

  async function handlePromote(spec: GateSpec) {
    setBusyId(spec.id);
    setFeedback((f) => ({ ...f, [spec.id]: { ok: true, msg: t('approving') } }));
    try {
      const res = await promoteSpec(spec.id);
      setFeedback((f) => ({
        ...f,
        [spec.id]: { ok: true, msg: t('promoted', { status: res.status, approver: res.approver }) },
      }));
      // Remove from the shadow queue once promoted.
      await mutate();
    } catch (e) {
      let msg = t('promote_failed');
      if (e instanceof ApiError) {
        const code = (e.body as { error?: { code?: string } })?.error?.code;
        if (code === 'SELF_APPROVAL_FORBIDDEN') {
          msg = t('self_approval');
        } else {
          msg = (e.body as { error?: { message?: string } })?.error?.message || t('error_status', { status: e.status });
        }
      } else if (e instanceof Error) {
        msg = e.message;
      }
      setFeedback((f) => ({ ...f, [spec.id]: { ok: false, msg } }));
    } finally {
      setBusyId(null);
    }
  }

  const list = specs ?? [];

  return (
    <div>
      {/* Queue when unlocked; only the lock banner when me/permissions says locked. */}
      <LockedScreen screen="aprobaciones">
        <Panel>
          <PanelHeader
            title={t('queue_title')}
            sub={t('queue_sub')}
          >
            <Badge variant="brand">{t('coi_badge')}</Badge>
          </PanelHeader>
          <PanelBody>
            {isLoading && (
              <div style={{ display: 'flex', justifyContent: 'center', padding: 24 }}>
                <Spinner size={20} />
              </div>
            )}

            {error && !isLoading && (
              <ErrorState onRetry={() => mutate()} />
            )}

            {!isLoading && !error && list.length === 0 && (
              <EmptyState />
            )}

            {!isLoading && !error && list.length > 0 && (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                {list.map((spec) => {
                  const isOwn = !!myId && (spec.authorId === myId || spec.userId === myId);
                  const fb = feedback[spec.id];
                  return (
                    <SpecRow
                      key={spec.id}
                      spec={spec}
                      isOwn={isOwn}
                      busy={busyId === spec.id}
                      feedback={fb}
                      onPromote={() => handlePromote(spec)}
                    />
                  );
                })}
              </div>
            )}
          </PanelBody>
        </Panel>
      </LockedScreen>
    </div>
  );
}

function SpecRow({
  spec,
  isOwn,
  busy,
  feedback,
  onPromote,
}: {
  spec: GateSpec;
  isOwn: boolean;
  busy: boolean;
  feedback?: { ok: boolean; msg: string };
  onPromote: () => void;
}) {
  const tc = useTranslations('Confirm');
  const t = useTranslations('PageApprovals');
  const [confirming, setConfirming] = useState(false);
  return (
    <div
      className="panel-inset"
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 14,
        padding: '12px 14px',
        flexWrap: 'wrap',
      }}
    >
      <div style={{ flex: 1, minWidth: 220 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
          <span style={{ fontWeight: 600, color: 'var(--tx-hi)', fontSize: 13.5 }}>
            {spec.name || spec.id}
          </span>
          <Badge variant="neutral">{spec.regime || '—'}</Badge>
          <Badge variant={spec.kind === 'ai' ? 'brand' : 'neutral'}>{spec.kind || '—'}</Badge>
          {spec.retired && <Badge variant="micro">{t('retired')}</Badge>}
        </div>
        <div
          className="mono"
          style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginTop: 4 }}
        >
          {t('author', { author: spec.author || spec.authorId || '—' })}
        </div>
      </div>

      {/* Validation metrics (backend-computed). */}
      <div style={{ display: 'flex', gap: 16, fontFamily: 'var(--mono)', fontSize: 11.5 }}>
        <Metric label="PF" value={spec.pf.toFixed(2)} />
        <Metric label={t('metric_cons')} value={`${(spec.cons * 100).toFixed(0)}%`} />
        <Metric label={t('metric_maxdd')} value={`${(spec.maxDrawdown * 100).toFixed(0)}%`} negative />
        <Metric label={t('metric_windows')} value={String(spec.windows)} />
      </div>

      <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: 6 }}>
        <div style={{ display: 'flex', gap: 8 }}>
          <button
            type="button"
            onClick={() => setConfirming(true)}
            disabled={isOwn || busy || confirming}
            title={isOwn ? t('own_title') : undefined}
            style={{
              ...primaryBtnStyle,
              opacity: isOwn || busy ? 0.5 : 1,
              cursor: isOwn || busy ? 'not-allowed' : 'pointer',
            }}
          >
            {busy ? t('approving') : t('approve_btn')}
          </button>
        </div>
        {confirming && !isOwn && (
          <ConfirmInline
            message={tc('promote')}
            confirmLabel={tc('promote_btn')}
            cancelLabel={tc('cancel')}
            busy={busy}
            onCancel={() => setConfirming(false)}
            onConfirm={() => {
              setConfirming(false);
              onPromote();
            }}
          />
        )}
        {isOwn && (
          <span className="mono" style={{ fontSize: 10, color: 'var(--micro, var(--tx-dim))' }}>
            {t('own_conflict')}
          </span>
        )}
        {feedback && (
          <span
            className="mono"
            style={{ fontSize: 10, color: feedback.ok ? 'var(--up)' : 'var(--down, var(--tx-dim))' }}
          >
            {feedback.msg}
          </span>
        )}
      </div>
    </div>
  );
}

function Metric({ label, value, negative }: { label: string; value: string; negative?: boolean }) {
  return (
    <div style={{ textAlign: 'center' }}>
      <div style={{ fontSize: 9, color: 'var(--tx-faint)', textTransform: 'uppercase', letterSpacing: '.05em' }}>
        {label}
      </div>
      <div style={{ color: negative ? 'var(--down, var(--tx))' : 'var(--tx-hi)', fontWeight: 600 }}>
        {value}
      </div>
    </div>
  );
}

function EmptyState() {
  const t = useTranslations('PageApprovals');
  return (
    <div style={{ textAlign: 'center', padding: '28px 12px', color: 'var(--tx-dim)' }}>
      <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--tx)' }}>
        {t('empty_title')}
      </div>
      <div style={{ fontSize: 12, marginTop: 6 }}>
        {t('empty_body')}
      </div>
    </div>
  );
}

function ErrorState({ onRetry }: { onRetry: () => void }) {
  const t = useTranslations('PageApprovals');
  return (
    <div style={{ textAlign: 'center', padding: '24px 12px' }}>
      <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--down, var(--tx-hi))' }}>
        {t('error_title')}
      </div>
      <div style={{ fontSize: 12, marginTop: 6, color: 'var(--tx-dim)' }}>
        {t('error_body')}
      </div>
      <button type="button" onClick={onRetry} style={{ ...ghostBtnStyle, marginTop: 12 }}>
        {t('retry')}
      </button>
    </div>
  );
}

const primaryBtnStyle: React.CSSProperties = {
  fontFamily: 'var(--sans)',
  fontSize: 12,
  fontWeight: 600,
  padding: '7px 12px',
  borderRadius: 'var(--r)',
  border: '1px solid var(--brand)',
  background: 'var(--brand)',
  color: '#0b0d12',
};

const ghostBtnStyle: React.CSSProperties = {
  fontFamily: 'var(--sans)',
  fontSize: 12,
  fontWeight: 600,
  padding: '7px 12px',
  borderRadius: 'var(--r)',
  border: '1px solid var(--line-2)',
  background: 'transparent',
  color: 'var(--tx)',
  cursor: 'pointer',
};
