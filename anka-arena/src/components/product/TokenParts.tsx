'use client';

// Presentational pieces of the agent-token screen (props in, callbacks out).

import { useState } from 'react';
import { useTranslations } from 'next-intl';
import { ConfirmInline } from '@/components/ui/ConfirmInline';
import { Badge } from '@/components/ui/Badge';
import { isTokenActive, type AgentToken } from '@/lib/product-api';
import { Notice } from './shared';

/** One-time display of a freshly created token value. */
export function TokenCreatedNotice({ value, onDismiss }: { value: string; onDismiss: () => void }) {
  const t = useTranslations('Product');
  const [copied, setCopied] = useState<'idle' | 'ok' | 'fail'>('idle');

  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
      setCopied('ok');
    } catch {
      setCopied('fail');
    }
  }

  return (
    <Notice tone="warn" title={t('tok_created_title')}>
      <div style={{ marginBottom: 8 }}>{t('tok_created_warning')}</div>
      <code
        className="mono"
        data-testid="token-value"
        style={{ display: 'block', padding: '8px 10px', background: 'var(--bg)', border: '1px solid var(--line-2)', borderRadius: 'var(--r)', fontSize: 12, overflowWrap: 'anywhere', userSelect: 'all' }}
      >
        {value}
      </code>
      <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginTop: 8, flexWrap: 'wrap' }}>
        <button type="button" className="btn sm primary" onClick={copy} style={{ fontSize: 12 }}>
          {t('tok_copy')}
        </button>
        <button type="button" className="btn sm" onClick={onDismiss} style={{ fontSize: 12 }}>
          {t('tok_created_done')}
        </button>
        {copied === 'ok' && <span className="mono" style={{ fontSize: 11, color: 'var(--up)' }}>{t('tok_copied')}</span>}
        {copied === 'fail' && <span className="mono" role="alert" style={{ fontSize: 11, color: 'var(--down)' }}>{t('tok_copy_failed')}</span>}
      </div>
    </Notice>
  );
}

function day(iso: string | null): string {
  return iso ? iso.slice(0, 10) : '—';
}

interface ListProps {
  tokens: AgentToken[];
  now?: number;
  /** Id awaiting revoke confirmation, if any. */
  pendingId: string | null;
  busy?: boolean;
  onAsk: (id: string) => void;
  onConfirm: (id: string) => void;
  onCancel: () => void;
}

/** Token rows without values: active first, inactive (revoked or expired) dimmed below. */
export function TokenList({ tokens, now, pendingId, busy, onAsk, onConfirm, onCancel }: ListProps) {
  const t = useTranslations('Product');
  const active = tokens.filter((x) => isTokenActive(x, now));
  const inactive = tokens.filter((x) => !isTokenActive(x, now));

  const row = (tk: AgentToken, isActive: boolean) => (
    <li
      key={tk.id}
      style={{ padding: '10px 0', borderTop: '1px solid var(--line)', opacity: isActive ? 1 : 0.6, listStyle: 'none' }}
    >
      <div style={{ display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap' }}>
        <b className="mono" style={{ fontSize: 13, color: 'var(--tx-hi)', overflowWrap: 'anywhere' }}>{tk.name}</b>
        <Badge variant={tk.profile === 'live' ? 'real' : tk.profile === 'paper' ? 'paper' : 'neutral'}>{tk.profile}</Badge>
        {!isActive && <Badge variant="neutral">{tk.revoked_at ? t('tok_revoked') : t('tok_expired')}</Badge>}
        {isActive && (
          <button
            type="button"
            className="btn sm"
            onClick={() => onAsk(tk.id)}
            disabled={busy}
            style={{ marginLeft: 'auto', fontSize: 11 }}
          >
            {t('tok_revoke')}
          </button>
        )}
      </div>
      <div className="mono" style={{ fontSize: 11, color: 'var(--tx-dim)', marginTop: 4, display: 'flex', gap: 14, flexWrap: 'wrap' }}>
        <span>{t('tok_created')}: {day(tk.created_at)}</span>
        <span>{t('tok_expires')}: {day(tk.expires_at)}</span>
        <span>{t('tok_last_used')}: {tk.last_used_at ? day(tk.last_used_at) : t('tok_never')}</span>
      </div>
      {pendingId === tk.id && (
        <div style={{ marginTop: 8 }}>
          <ConfirmInline
            message={t('tok_revoke_confirm', { name: tk.name })}
            confirmLabel={t('tok_revoke_yes')}
            cancelLabel={t('tok_revoke_no')}
            onConfirm={() => onConfirm(tk.id)}
            onCancel={onCancel}
            busy={busy}
          />
        </div>
      )}
    </li>
  );

  if (tokens.length === 0) {
    return <div className="mono" style={{ fontSize: 12, color: 'var(--tx-faint)' }}>{t('tok_empty')}</div>;
  }
  return (
    <ul style={{ margin: 0, padding: 0 }}>
      {active.length === 0 && <div className="mono" style={{ fontSize: 12, color: 'var(--tx-faint)' }}>{t('tok_no_active')}</div>}
      {active.map((x) => row(x, true))}
      {inactive.map((x) => row(x, false))}
    </ul>
  );
}
