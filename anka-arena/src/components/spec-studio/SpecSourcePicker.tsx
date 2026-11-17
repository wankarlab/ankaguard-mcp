'use client';

import React, { useState } from 'react';
import { Panel, PanelHeader, PanelBody } from '@/components/ui/Panel';
import type { SpecTemplate, GateSpec } from '@/lib/adapters';

// ── SpecSourcePicker ────────────────────────────────────────────────────────
// C0.5: the editor opens from BACKEND data — a seeded template or an existing own
// spec. This selector is the only entry point; there is no client DEFAULT_SPEC.

interface SpecSourcePickerProps {
  templates:      SpecTemplate[];
  mySpecs:        GateSpec[];
  loading:        boolean;
  error:          string | null;
  activeSpecId:   string | null;
  onPickTemplate: (t: SpecTemplate) => void;
  onPickOwnSpec:  (g: GateSpec) => void;
}

type Tab = 'templates' | 'mine';

function regimeColor(regime: string): string {
  switch (regime.toUpperCase()) {
    case 'TRENDING': return 'var(--up)';
    case 'RANGING':  return 'var(--paper)';
    case 'BREAKOUT': return 'var(--micro)';
    default:         return 'var(--tx-dim)';
  }
}

export function SpecSourcePicker({
  templates,
  mySpecs,
  loading,
  error,
  activeSpecId,
  onPickTemplate,
  onPickOwnSpec,
}: SpecSourcePickerProps) {
  const [tab, setTab] = useState<Tab>('templates');

  const tabs: Array<{ key: Tab; label: string; count: number }> = [
    { key: 'templates', label: 'Plantillas',    count: templates.length },
    { key: 'mine',      label: 'Mis specs',     count: mySpecs.length },
  ];

  return (
    <div style={{ marginBottom: 16 }}>
      <Panel>
        <PanelHeader title="Abrir desde" sub="plantilla del backend o spec propia" />
        <PanelBody>
          {/* Tab strip */}
          <div style={{ display: 'flex', gap: 6, marginBottom: 12 }}>
            {tabs.map((t) => {
              const active = tab === t.key;
              return (
                <button
                  key={t.key}
                  onClick={() => setTab(t.key)}
                  style={{
                    fontFamily:    'var(--mono)',
                    fontSize:      11,
                    letterSpacing: '0.04em',
                    padding:       '5px 12px',
                    borderRadius:  6,
                    border:        `1px solid ${active ? 'var(--brand)' : 'var(--line-2)'}`,
                    background:    active ? 'var(--brand-soft)' : 'var(--bg-inset)',
                    color:         active ? 'var(--brand)' : 'var(--tx-dim)',
                    cursor:        'pointer',
                  }}
                >
                  {t.label}
                  <span className="num" style={{ marginLeft: 6, color: 'var(--tx-faint)' }}>{t.count}</span>
                </button>
              );
            })}
          </div>

          {loading && (
            <div className="dim" style={{ fontSize: 12, padding: '8px 2px' }}>Cargando del backend…</div>
          )}
          {error && !loading && (
            <div style={{ fontSize: 12, padding: '8px 2px', color: 'var(--down)' }}>{error}</div>
          )}

          {/* Templates list */}
          {!loading && tab === 'templates' && (
            templates.length === 0 ? (
              <div className="dim" style={{ fontSize: 12, padding: '8px 2px' }}>
                No hay plantillas en el backend todavía.
              </div>
            ) : (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                {templates.map((t) => (
                  <button
                    key={t.templateId}
                    onClick={() => onPickTemplate(t)}
                    style={{
                      textAlign:    'left',
                      padding:      '10px 12px',
                      borderRadius: 8,
                      border:       '1px solid var(--line)',
                      background:   'var(--bg-inset)',
                      cursor:       'pointer',
                      display:      'flex',
                      alignItems:   'center',
                      gap:          10,
                    }}
                  >
                    <div style={{ flex: 1, minWidth: 0 }}>
                      <div style={{ fontSize: 12.5, color: 'var(--tx-hi)', fontWeight: 600 }}>{t.name}</div>
                      {t.description && (
                        <div className="dim" style={{ fontSize: 11, marginTop: 2 }}>{t.description}</div>
                      )}
                    </div>
                    {t.regime && (
                      <span
                        className="mono"
                        style={{
                          fontSize:      10,
                          letterSpacing: '0.05em',
                          color:         regimeColor(t.regime),
                          border:        `1px solid ${regimeColor(t.regime)}`,
                          borderRadius:  5,
                          padding:       '2px 7px',
                          flex:          'none',
                        }}
                      >
                        {t.regime.toUpperCase()}
                      </span>
                    )}
                  </button>
                ))}
              </div>
            )
          )}

          {/* Own specs list */}
          {!loading && tab === 'mine' && (
            mySpecs.length === 0 ? (
              <div className="dim" style={{ fontSize: 12, padding: '8px 2px' }}>
                Aún no has creado specs. Empieza desde una plantilla.
              </div>
            ) : (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                {mySpecs.map((g) => {
                  const active = activeSpecId === g.id;
                  return (
                    <button
                      key={g.id}
                      onClick={() => onPickOwnSpec(g)}
                      style={{
                        textAlign:    'left',
                        padding:      '10px 12px',
                        borderRadius: 8,
                        border:       `1px solid ${active ? 'var(--brand)' : 'var(--line)'}`,
                        background:   active ? 'var(--brand-soft)' : 'var(--bg-inset)',
                        cursor:       'pointer',
                        display:      'flex',
                        alignItems:   'center',
                        gap:          10,
                      }}
                    >
                      <div style={{ flex: 1, minWidth: 0 }}>
                        <div style={{ fontSize: 12.5, color: 'var(--tx-hi)', fontWeight: 600 }}>{g.name}</div>
                        <div className="dim mono" style={{ fontSize: 10, marginTop: 2 }}>
                          {g.status || 'draft'}{g.retired ? ' · RETIRADA' : ''}
                        </div>
                      </div>
                      {g.regime && (
                        <span
                          className="mono"
                          style={{
                            fontSize:      10,
                            letterSpacing: '0.05em',
                            color:         regimeColor(g.regime),
                            border:        `1px solid ${regimeColor(g.regime)}`,
                            borderRadius:  5,
                            padding:       '2px 7px',
                            flex:          'none',
                          }}
                        >
                          {g.regime.toUpperCase()}
                        </span>
                      )}
                    </button>
                  );
                })}
              </div>
            )
          )}
        </PanelBody>
      </Panel>
    </div>
  );
}
