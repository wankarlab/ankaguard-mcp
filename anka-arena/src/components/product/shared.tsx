import type { CSSProperties, ReactNode } from 'react';

export const inputStyle: CSSProperties = {
  width: '100%',
  background: 'var(--bg)',
  border: '1px solid var(--line-2)',
  borderRadius: 'var(--r)',
  color: 'var(--tx)',
  padding: '8px 10px',
  fontFamily: 'var(--mono)',
  fontSize: 12,
  outline: 'none',
};

export const labelStyle: CSSProperties = {
  fontSize: 10,
  letterSpacing: '.06em',
  textTransform: 'uppercase',
  color: 'var(--tx-faint)',
  marginBottom: 4,
  display: 'block',
};

type Tone = 'warn' | 'error' | 'info' | 'ok';
const TONE: Record<Tone, { c: string; bg: string }> = {
  warn: { c: 'var(--warn)', bg: 'rgba(233,162,62,.08)' },
  error: { c: 'var(--down)', bg: 'rgba(233,90,90,.07)' },
  info: { c: 'var(--tx-dim)', bg: 'var(--bg-inset)' },
  ok: { c: 'var(--up)', bg: 'rgba(54,205,134,.07)' },
};

/** Inline notice box; errors use role=alert so they are announced. */
export function Notice({ tone, children, title }: { tone: Tone; children: ReactNode; title?: string }) {
  const { c, bg } = TONE[tone];
  return (
    <div
      role={tone === 'error' ? 'alert' : 'status'}
      style={{ padding: '10px 12px', borderRadius: 'var(--r)', border: `1px solid ${c}`, background: bg, fontSize: 12.5, lineHeight: 1.5, color: 'var(--tx)' }}
    >
      {title && <div style={{ fontWeight: 700, color: c, marginBottom: 2 }}>{title}</div>}
      {children}
    </div>
  );
}

export function PageHeader({ eyebrow, title }: { eyebrow: string; title: string }) {
  return (
    <div style={{ marginBottom: 20 }}>
      <div className="mono" style={{ fontSize: 10, letterSpacing: '.08em', color: 'var(--brand)', textTransform: 'uppercase', marginBottom: 4 }}>
        {eyebrow}
      </div>
      <h2 style={{ margin: 0, fontSize: 20, color: 'var(--tx-hi)', fontWeight: 700 }}>{title}</h2>
    </div>
  );
}
