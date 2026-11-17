'use client';

import React, { useState, useCallback, useEffect } from 'react';
import { useTranslations } from 'next-intl';
import useSWR from 'swr';
import { Panel, PanelHeader, PanelBody } from '@/components/ui/Panel';
import { Badge } from '@/components/ui/Badge';
import { BlockEditor }   from '@/components/spec-studio/BlockEditor';
import { YamlMirror }    from '@/components/spec-studio/YamlMirror';
import { CopilotPanel }  from '@/components/spec-studio/CopilotPanel';
import { BacktestBar }   from '@/components/spec-studio/BacktestBar';
import { SpecSourcePicker } from '@/components/spec-studio/SpecSourcePicker';
import {
  type SpecState,
  specStateFromTemplate,
  specStateFromSpecDoc,
  fetchSkinInGamePolicy,
} from '@/lib/spec-studio';
import { fetchSpecTemplates, fetchSpecs } from '@/lib/api';
import type { SpecTemplate, GateSpec } from '@/lib/adapters';

// NOTE: SpecState lives in '@/lib/spec-studio' (shared by all subcomponents).
// It is intentionally NOT defined here — importing it from the page module
// breaks under [locale] routing.

// ── Bot icon (inline SVG) ─────────────────────────────────────────────────────

function IconBot() {
  return (
    <svg width="11" height="11" viewBox="0 0 14 14" fill="none">
      <rect x="2" y="4" width="10" height="8" rx="1.5" stroke="currentColor" strokeWidth="1.2" />
      <path d="M5 8h4M5 10h2" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" />
      <path d="M7 4V2M5 2h4" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" />
      <circle cx="5" cy="7" r="0.8" fill="currentColor" />
      <circle cx="9" cy="7" r="0.8" fill="currentColor" />
    </svg>
  );
}

// ── API panel (bottom-right reference) ───────────────────────────────────────

function ApiReferencePanel() {
  const t = useTranslations('SpecStudio');
  return (
    <Panel>
      <PanelHeader title={t('api_title')} />
      <PanelBody>
        <pre className="code" style={{ margin: 0, fontSize: 10.5 }}>{`POST /api/v1/specs
{ "spec": <yaml>, "author_id": … }
→ 201 { id, content_hash, status:"queued" }

GET /api/v1/specs/:id/copilot
→ 200 { suggestion: { text, mutation } }

POST /api/v1/specs/:id/backtest
→ 200 { result: { profit_factor, win_rate } }`}</pre>
        <div className="mono faint" style={{ fontSize: 9.5, marginTop: 8 }}>
          {t('api_note')}
        </div>
      </PanelBody>
    </Panel>
  );
}

// ── Page ─────────────────────────────────────────────────────────────────────

export default function SpecStudioPage() {
  const t = useTranslations('SpecStudio');
  // C0.5: the editor opens from BACKEND data only (a template or an existing own
  // spec) — never a client constant. `spec` is null until a source is chosen.
  const [spec,   setSpec]   = useState<SpecState | null>(null);
  const [specId, setSpecId] = useState<string | null>(null);

  // C2.2: content_hash is computed BY THE BACKEND. We only ever display a hash
  // returned by the backend (on load of an existing spec, or after sending to the
  // gate). For an unsaved draft there is no backend hash yet.
  const [backendHash, setBackendHash] = useState<string | null>(null);

  // ── Sources (templates + own specs), loaded from the backend ──────────────
  const { data: templates, isLoading: tplLoading, error: tplError } =
    useSWR<SpecTemplate[]>('spec-templates', fetchSpecTemplates);
  const { data: mySpecs, isLoading: mineLoading, error: mineError } =
    useSWR<GateSpec[]>('spec-mine', () => fetchSpecs({ mine: true }));

  // ── Skin-in-the-game threshold (dynamic policy) ──────────────────────────
  const { data: skin } = useSWR('skin-in-game', fetchSkinInGamePolicy);

  // Auto-select the first template once loaded if nothing chosen yet.
  useEffect(() => {
    if (spec === null && templates && templates.length > 0) {
      setSpec(specStateFromTemplate(templates[0]));
      setSpecId(null);
      setBackendHash(null);
    }
  }, [templates, spec]);

  // Typed updater — keeps SpecState fully typed
  const updateSpec = useCallback(<K extends keyof SpecState>(key: K, value: SpecState[K]) => {
    setSpec((prev) => (prev ? { ...prev, [key]: value } : prev));
    // Any local edit invalidates the backend hash (it must be recomputed on send).
    setBackendHash(null);
  }, []);

  // Batch-apply a partial mutation (from copilot)
  const applyMutation = useCallback((mutation: Partial<SpecState>) => {
    setSpec((prev) => (prev ? { ...prev, ...mutation } : prev));
    setBackendHash(null);
  }, []);

  // ── Source selection handlers ─────────────────────────────────────────────
  const handlePickTemplate = useCallback((t: SpecTemplate) => {
    setSpec(specStateFromTemplate(t));
    setSpecId(null);
    setBackendHash(null);
  }, []);

  const handlePickOwnSpec = useCallback((g: GateSpec) => {
    // The /specs list row (GateSpec) carries name + regime — enough to seed the
    // editor from an existing own spec. The backend remains the source of truth
    // for the spec's content_hash (shown once re-sent).
    setSpec(
      specStateFromSpecDoc({
        metadata: { name: g.name },
        regime:   { allowed_regimes: g.regime ? [g.regime] : [] },
      }),
    );
    setSpecId(g.id);
    setBackendHash(null);
  }, []);

  // ── Send-to-gate callback: capture the backend content_hash ───────────────
  const handleSpecCreated = useCallback((id: string, hash?: string) => {
    setSpecId(id);
    if (hash) setBackendHash(hash);
  }, []);

  const sourcesLoading = tplLoading || mineLoading;
  const sourcesError   = tplError || mineError;

  return (
    <div className="content-pad enter">
      <div className="maxw">

        {/* ── Header ── */}
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 20, flexWrap: 'wrap', gap: 10 }}>
          <div>
            <div className="eyebrow" style={{ color: 'var(--brand)' }}>{t('eyebrow')}</div>
            <h2 style={{ margin: '4px 0 0', fontSize: 17, color: 'var(--tx-hi)', fontWeight: 600 }}>
              {t('title')}
            </h2>
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <Badge variant="brand">
              <IconBot /> {t('copilot_on')}
            </Badge>
            <span className="dim mono" style={{ fontSize: 10 }}>
              {t('no_go')}
            </span>
          </div>
        </div>

        {/* ── Source picker: template or own spec (C0.5) ── */}
        <SpecSourcePicker
          templates={templates ?? []}
          mySpecs={mySpecs ?? []}
          loading={sourcesLoading}
          error={sourcesError ? t('sources_error') : null}
          activeSpecId={specId}
          onPickTemplate={handlePickTemplate}
          onPickOwnSpec={handlePickOwnSpec}
        />

        {/* ── Empty state: no source chosen and no templates ── */}
        {spec === null ? (
          <Panel>
            <PanelBody>
              <div className="dim" style={{ padding: 20, fontSize: 12.5, textAlign: 'center' }}>
                {sourcesLoading
                  ? t('loading_templates')
                  : sourcesError
                    ? t('templates_unavailable')
                    : t('no_sources')}
              </div>
            </PanelBody>
          </Panel>
        ) : (

        /* ── Two-column layout ── */
        <div style={{
          display:             'grid',
          gridTemplateColumns: 'minmax(0,1fr) 360px',
          gap:                 16,
          alignItems:          'start',
        }}>

          {/* ── Left: editor + YAML ── */}
          <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>

            <div style={{
              display:             'grid',
              gridTemplateColumns: '1fr 1fr',
              gap:                 14,
            }}>
              {/* Block editor */}
              <Panel>
                <PanelHeader title={t('block_editor')} sub={t('block_editor_sub')} />
                <BlockEditor spec={spec} onChange={updateSpec} />
              </Panel>

              {/* YAML mirror */}
              <Panel>
                <PanelHeader title={t('yaml_view')} sub={t('yaml_view_sub')} />
                <YamlMirror spec={spec} backendHash={backendHash} />
              </Panel>
            </div>

            {/* Backtest + gate bar */}
            <Panel>
              <BacktestBar
                spec={spec}
                specId={specId}
                skinInGameDays={skin?.days ?? null}
                onSpecCreated={handleSpecCreated}
              />
            </Panel>

          </div>

          {/* ── Right: copilot + API reference ── */}
          <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>

            <Panel>
              <PanelHeader
                title={t('copilot_title')}
                sub={t('copilot_sub')}
              >
                <Badge variant="brand">
                  <IconBot />
                </Badge>
              </PanelHeader>
              <CopilotPanel
                spec={spec}
                specId={specId}
                onApply={applyMutation}
              />
            </Panel>

            <ApiReferencePanel />

          </div>

        </div>
        )}
      </div>
    </div>
  );
}
