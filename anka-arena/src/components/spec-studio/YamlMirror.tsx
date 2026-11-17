'use client';

import React, { useMemo } from 'react';
import type { SpecState } from '@/lib/spec-studio';

// ── YAML generator ───────────────────────────────────────────────────────────
// C2.2: content_hash is computed BY THE BACKEND. The FE never derives it. When a
// hash is not yet available (unsaved draft) the YAML omits it and the lock badge
// shows "se calcula al enviar".

function buildYaml(spec: SpecState, hash: string | null): string {
  const hashLine = hash
    ? `content_hash: ${hash.slice(0, 12)}…`
    : `# content_hash: lo calcula el backend al enviar al gate`;
  return `name: ${spec.name}
regime: [${spec.regime}]
entry:
  rsi:
    min: ${spec.rsiMin}
    max: ${spec.rsiMax}
  trend_filter: ema50_1h
exit:
  tp_pct: ${spec.tp.toFixed(1)}
  sl_pct: ${spec.sl.toFixed(1)}
  trailing: ${spec.trail}
  max_hold_min: ${spec.maxHold}
risk:
  max_order_usd: 2.0   # cap de testing
${hashLine}`;
}

// ── Syntax highlighter (manual, no external lib) ─────────────────────────────
// Token types: key | value | comment | number | boolean | string

type Token = { type: 'key' | 'num' | 'bool' | 'comment' | 'str' | 'plain'; text: string };

function tokenizeLine(line: string): Token[] {
  // Comment
  if (/^\s*#/.test(line)) {
    return [{ type: 'comment', text: line }];
  }

  const tokens: Token[] = [];

  // Inline comment
  const commentIdx = line.indexOf('#');
  const main   = commentIdx !== -1 ? line.slice(0, commentIdx) : line;
  const comment = commentIdx !== -1 ? line.slice(commentIdx) : '';

  const colonIdx = main.indexOf(':');
  if (colonIdx !== -1) {
    const keyPart = main.slice(0, colonIdx + 1);       // includes ":"
    const valPart = main.slice(colonIdx + 1);           // after ":"

    tokens.push({ type: 'key', text: keyPart });

    if (valPart.trim() !== '') {
      const trimmed = valPart.trim();
      if (/^-?\d+(\.\d+)?$/.test(trimmed)) {
        tokens.push({ type: 'num', text: valPart });
      } else if (trimmed === 'true' || trimmed === 'false') {
        tokens.push({ type: 'bool', text: valPart });
      } else {
        tokens.push({ type: 'str', text: valPart });
      }
    }
  } else {
    tokens.push({ type: 'plain', text: main });
  }

  if (comment) {
    tokens.push({ type: 'comment', text: comment });
  }

  return tokens;
}

const TOKEN_COLOR: Record<Token['type'], string> = {
  key:     'var(--brand)',
  num:     'var(--micro)',
  bool:    'var(--up)',
  str:     'var(--tx-hi)',
  comment: 'var(--tx-faint)',
  plain:   'var(--tx)',
};

function HighlightedYaml({ yaml }: { yaml: string }) {
  const lines = yaml.split('\n');
  return (
    <pre
      style={{
        fontFamily:  'var(--mono)',
        fontSize:    11.5,
        lineHeight:  1.75,
        background:  'var(--bg-void)',
        border:      '1px solid var(--line)',
        borderRadius: 'var(--r)',
        padding:     '14px 16px',
        margin:      0,
        overflowX:   'auto',
        whiteSpace:  'pre',
        userSelect:  'text',
      }}
    >
      {lines.map((line, li) => (
        <span key={li} style={{ display: 'block' }}>
          {tokenizeLine(line).map((tok, ti) => (
            <span key={ti} style={{ color: TOKEN_COLOR[tok.type] }}>
              {tok.text}
            </span>
          ))}
        </span>
      ))}
    </pre>
  );
}

// ── YamlMirror ───────────────────────────────────────────────────────────────

interface YamlMirrorProps {
  spec: SpecState;
  /** content_hash returned by the backend; null for an unsaved/edited draft. */
  backendHash?: string | null;
}

export function YamlMirror({ spec, backendHash = null }: YamlMirrorProps) {
  const yaml = useMemo(() => buildYaml(spec, backendHash), [spec, backendHash]);

  return (
    <div style={{ padding: 14 }}>
      {/* Hash badge row */}
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 10 }}>
        <span className="eyebrow">vista yaml · read-only</span>
        <span
          className="badge neutral"
          title={backendHash ? `content hash (backend): ${backendHash}` : 'el backend calcula el content_hash al enviar al gate'}
          style={{ fontFamily: 'var(--mono)', fontSize: 10, letterSpacing: '0.04em', cursor: 'default' }}
        >
          {/* Lock icon inline SVG */}
          <svg width="10" height="10" viewBox="0 0 12 12" fill="none" style={{ marginRight: 3 }}>
            <rect x="2" y="5.5" width="8" height="6" rx="1" stroke="currentColor" strokeWidth="1.2" />
            <path d="M4 5.5V4a2 2 0 1 1 4 0v1.5" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" />
            <circle cx="6" cy="8.5" r="0.75" fill="currentColor" />
          </svg>
          {backendHash ? backendHash.slice(0, 8) : 'sin firmar'}
        </span>
      </div>

      <HighlightedYaml yaml={yaml} />

      <div style={{ display: 'flex', gap: 8, marginTop: 10, flexWrap: 'wrap' }}>
        <span className="badge brand" style={{ fontSize: 9.5 }}>inmutable</span>
        <span className="dim mono" style={{ fontSize: 9.5 }}>
          spec.go: ContentHash() · lo calcula el backend, no el cliente
        </span>
      </div>
    </div>
  );
}
