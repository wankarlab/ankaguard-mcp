'use client';

/**
 * ConsentModal — blocking legal-consent gate (C1.3).
 *
 * Flow (NO localStorage — backend is the source of truth):
 *  1. GET /consent/template/:kind → renders the versioned legal markdown body.
 *  2. User must scroll to the bottom AND tick the checkbox to enable "Accept".
 *  3. POST /consent { kind, template_version } records the acceptance (idempotent).
 *  4. GET /consent/status?kind=... verifies the gate (acceptedVersion >= version).
 *
 * The legal text, its version, and the acceptance audit trail all live in the
 * DB. Nothing legal is hardcoded in the client. Publishing a higher template
 * version forces re-consent automatically (version comparison in the store).
 */

import React from 'react';
import { useTranslations } from 'next-intl';
import { fetchConsentTemplate } from '@/lib/api';
import type { ConsentTemplate } from '@/lib/adapters';
import { useConsentStore } from '@/stores/consent';

interface ConsentModalProps {
  /** Consent kind, e.g. "copy_trading". */
  kind: string;
  /** Whether the modal is open / gating an action. */
  open: boolean;
  /** Called after the consent is successfully recorded. */
  onAccepted: () => void;
  /** Called when the user dismisses without accepting (action is cancelled). */
  onCancel: () => void;
  /** Optional heading override. */
  title?: string;
}

// ── Minimal markdown renderer (headings/bold/lists/paragraphs) ──────────────────
// Avoids pulling a markdown dependency for short legal documents.

function renderMarkdown(md: string): React.ReactNode {
  const lines = md.replace(/\r\n/g, '\n').split('\n');
  const blocks: React.ReactNode[] = [];
  let list: string[] = [];

  const flushList = (key: string) => {
    if (list.length === 0) return;
    const items = list;
    list = [];
    blocks.push(
      <ul key={key} style={{ margin: '8px 0', paddingLeft: 20, color: 'var(--tx)' }}>
        {items.map((it, i) => (
          <li key={i} style={{ marginBottom: 4, fontSize: 13, lineHeight: 1.55 }}>
            {renderInline(it)}
          </li>
        ))}
      </ul>,
    );
  };

  lines.forEach((raw, idx) => {
    const line = raw.trimEnd();
    const key = `b${idx}`;
    if (/^#{1,6}\s+/.test(line)) {
      flushList(`${key}-l`);
      const level = (line.match(/^#+/) ?? ['#'])[0].length;
      const text = line.replace(/^#+\s+/, '');
      const size = level <= 1 ? 17 : level === 2 ? 15 : 13.5;
      blocks.push(
        <div
          key={key}
          style={{
            fontFamily: 'var(--sans)',
            fontSize: size,
            fontWeight: 700,
            color: 'var(--tx-hi)',
            margin: '14px 0 6px',
          }}
        >
          {renderInline(text)}
        </div>,
      );
    } else if (/^\s*[-*]\s+/.test(line)) {
      list.push(line.replace(/^\s*[-*]\s+/, ''));
    } else if (line.trim() === '') {
      flushList(`${key}-l`);
    } else {
      flushList(`${key}-l`);
      blocks.push(
        <p key={key} style={{ margin: '6px 0', fontSize: 13, lineHeight: 1.6, color: 'var(--tx)' }}>
          {renderInline(line)}
        </p>,
      );
    }
  });
  flushList('tail-l');
  return blocks;
}

function renderInline(text: string): React.ReactNode {
  // Bold (**x**) only — keep it minimal and safe.
  const parts = text.split(/(\*\*[^*]+\*\*)/g);
  return parts.map((p, i) => {
    if (/^\*\*[^*]+\*\*$/.test(p)) {
      return (
        <strong key={i} style={{ color: 'var(--tx-hi)' }}>
          {p.slice(2, -2)}
        </strong>
      );
    }
    return <React.Fragment key={i}>{p}</React.Fragment>;
  });
}

export function ConsentModal({ kind, open, onAccepted, onCancel, title }: ConsentModalProps) {
  const t = useTranslations('Consent');
  const accept = useConsentStore((s) => s.accept);

  const [template, setTemplate] = React.useState<ConsentTemplate | null>(null);
  const [loading, setLoading]   = React.useState(false);
  const [error, setError]       = React.useState<string | null>(null);
  const [scrolledToEnd, setScrolledToEnd] = React.useState(false);
  const [checked, setChecked]   = React.useState(false);
  const [submitting, setSubmitting] = React.useState(false);

  // Load the template whenever the modal opens for a kind.
  React.useEffect(() => {
    if (!open) return;
    let cancelled = false;
    setLoading(true);
    setError(null);
    setScrolledToEnd(false);
    setChecked(false);
    fetchConsentTemplate(kind)
      .then((tpl) => {
        if (cancelled) return;
        setTemplate(tpl);
      })
      .catch((e) => {
        if (cancelled) return;
        setError(e instanceof Error ? e.message : t('load_error'));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, kind, t]);

  const onScroll = React.useCallback((e: React.UIEvent<HTMLDivElement>) => {
    const el = e.currentTarget;
    if (el.scrollHeight - el.scrollTop - el.clientHeight < 24) {
      setScrolledToEnd(true);
    }
  }, []);

  const submit = React.useCallback(async () => {
    if (!template || !checked || !scrolledToEnd || submitting) return;
    setSubmitting(true);
    setError(null);
    try {
      await accept(kind, template.version);
      onAccepted();
    } catch (e) {
      setError(e instanceof Error ? e.message : t('record_error'));
    } finally {
      setSubmitting(false);
    }
  }, [template, checked, scrolledToEnd, submitting, accept, kind, onAccepted, t]);

  if (!open) return null;

  const canAccept = checked && scrolledToEnd && !!template && !submitting;

  return (
    <>
      {/* Scrim */}
      <div
        onClick={onCancel}
        aria-hidden
        style={{
          position: 'fixed',
          inset: 0,
          background: 'rgba(6,8,12,0.6)',
          backdropFilter: 'blur(3px)',
          zIndex: 300,
        }}
      />
      {/* Modal */}
      <div
        role="dialog"
        aria-modal="true"
        aria-label={title ?? t('aria_label')}
        style={{
          position: 'fixed',
          top: '50%',
          left: '50%',
          transform: 'translate(-50%, -50%)',
          width: 560,
          maxWidth: '94vw',
          maxHeight: '88vh',
          display: 'flex',
          flexDirection: 'column',
          background: 'var(--bg-panel)',
          border: '1px solid var(--line-2)',
          borderRadius: 'var(--r)',
          boxShadow: '0 24px 80px rgba(0,0,0,0.55)',
          zIndex: 301,
        }}
      >
        {/* Header */}
        <div
          style={{
            padding: '16px 20px',
            borderBottom: '1px solid var(--line)',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            gap: 12,
          }}
        >
          <div style={{ fontFamily: 'var(--sans)', fontSize: 15, fontWeight: 700, color: 'var(--tx-hi)' }}>
            {title ?? t('title_required')}
            {template ? (
              <span style={{ fontFamily: 'var(--mono)', fontSize: 10.5, color: 'var(--tx-faint)', marginLeft: 8 }}>
                v{template.version}
              </span>
            ) : null}
          </div>
          <button
            onClick={onCancel}
            aria-label={t('close')}
            style={{
              width: 28,
              height: 28,
              borderRadius: 'var(--r-sm, 6px)',
              border: '1px solid var(--line)',
              background: 'var(--bg-inset)',
              color: 'var(--tx-dim)',
              cursor: 'pointer',
              fontSize: 16,
              lineHeight: 1,
            }}
          >
            ×
          </button>
        </div>

        {/* Body — scrollable legal text */}
        <div
          onScroll={onScroll}
          style={{
            flex: 1,
            overflowY: 'auto',
            padding: '16px 20px',
          }}
        >
          {loading && (
            <div style={{ fontFamily: 'var(--mono)', fontSize: 12, color: 'var(--tx-dim)' }}>
              {t('loading_doc')}
            </div>
          )}
          {!loading && error && (
            <div
              style={{
                padding: 14,
                borderRadius: 'var(--r)',
                border: '1px solid rgba(240,86,79,0.3)',
                background: 'var(--real-soft, rgba(240,86,79,0.08))',
                color: 'var(--down)',
                fontFamily: 'var(--mono)',
                fontSize: 12,
              }}
            >
              {error}
            </div>
          )}
          {!loading && !error && template && renderMarkdown(template.bodyMd)}
        </div>

        {/* Footer — checkbox + actions */}
        <div style={{ borderTop: '1px solid var(--line)', padding: '14px 20px' }}>
          {!scrolledToEnd && template && !loading && (
            <div
              style={{
                fontFamily: 'var(--mono)',
                fontSize: 10.5,
                color: 'var(--tx-faint)',
                marginBottom: 10,
                textAlign: 'center',
              }}
            >
              {t('scroll_to_end')}
            </div>
          )}
          <label
            style={{
              display: 'flex',
              alignItems: 'flex-start',
              gap: 10,
              cursor: scrolledToEnd ? 'pointer' : 'default',
              opacity: scrolledToEnd ? 1 : 0.5,
              marginBottom: 12,
            }}
          >
            <input
              type="checkbox"
              checked={checked}
              disabled={!scrolledToEnd}
              onChange={(e) => setChecked(e.target.checked)}
              style={{ marginTop: 2 }}
            />
            <span style={{ fontFamily: 'var(--sans)', fontSize: 12.5, color: 'var(--tx)' }}>
              {t('accept_terms')}
            </span>
          </label>
          <div style={{ display: 'flex', gap: 10 }}>
            <button
              onClick={onCancel}
              style={{
                flex: 1,
                padding: '9px',
                borderRadius: 'var(--r)',
                border: '1px solid var(--line)',
                background: 'transparent',
                color: 'var(--tx-dim)',
                cursor: 'pointer',
                fontFamily: 'var(--mono)',
                fontSize: 12,
              }}
            >
              {t('cancel')}
            </button>
            <button
              onClick={submit}
              disabled={!canAccept}
              style={{
                flex: 2,
                padding: '9px',
                borderRadius: 'var(--r)',
                border: 'none',
                background: canAccept
                  ? 'linear-gradient(180deg, var(--brand), var(--brand-2))'
                  : 'var(--bg-panel-2, var(--bg-inset))',
                color: canAccept ? '#fff' : 'var(--tx-faint)',
                cursor: canAccept ? 'pointer' : 'default',
                fontFamily: 'var(--mono)',
                fontSize: 12,
                fontWeight: 600,
                opacity: submitting ? 0.7 : 1,
              }}
            >
              {submitting ? t('recording') : t('accept_continue')}
            </button>
          </div>
        </div>
      </div>
    </>
  );
}

export default ConsentModal;
