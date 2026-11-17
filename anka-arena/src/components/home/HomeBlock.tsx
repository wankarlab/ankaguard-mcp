'use client';

// HomeBlock — shared frame for every /home block: title, optional detail link
// and a body. Each block owns its data, so one failing block never affects
// the others.

import type { ReactNode } from 'react';
import { Link } from '@/i18n/routing';
import { Panel, PanelHeader, PanelBody } from '@/components/ui/Panel';

interface Props {
  title: string;
  /** Locale-aware href (via next-intl Link). */
  href?: string;
  linkLabel?: string;
  children: ReactNode;
}

export function HomeBlock({ title, href, linkLabel, children }: Props) {
  return (
    <Panel>
      <PanelHeader title={title}>
        {href && linkLabel && (
          <Link href={href} className="mono" style={{ fontSize: 11, color: 'var(--brand)', textDecoration: 'none' }}>
            {linkLabel} →
          </Link>
        )}
      </PanelHeader>
      <PanelBody>{children}</PanelBody>
    </Panel>
  );
}

export function BlockMessage({ text, tone }: { text: string; tone?: 'error' }) {
  return (
    <div className="mono" role={tone === 'error' ? 'alert' : undefined} style={{ fontSize: 12, padding: '8px 0', color: tone === 'error' ? 'var(--down)' : 'var(--tx-faint)' }}>
      {text}
    </div>
  );
}

export function Stat({ label, value, sub, color }: { label: string; value: string; sub?: string; color?: string }) {
  return (
    <div className="panel-inset" style={{ padding: '10px 12px' }}>
      <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', textTransform: 'uppercase', letterSpacing: '.06em' }}>{label}</div>
      <div className="mono" style={{ fontSize: 18, fontWeight: 700, color: color ?? 'var(--tx-hi)', marginTop: 4 }}>{value}</div>
      {sub && <div className="mono" style={{ fontSize: 11, color: color ?? 'var(--tx-dim)', marginTop: 2 }}>{sub}</div>}
    </div>
  );
}
