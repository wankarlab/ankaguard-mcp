'use client';

// Teatro del Gate — FRONTEND §3.3 · el árbitro soberano
// Datos 100% reales via SWR. CERO datos falsos: si el backend falla, se
// renderiza un panel de error con retry y las acciones quedan deshabilitadas
// (el usuario JAMÁS ve datos que no vengan del backend).

import { useState } from 'react';
import { useTranslations } from 'next-intl';
import useSWR from 'swr';
import { apiFetch } from '@/lib/api';
import { GateQueue } from '@/components/gate/GateQueue';
import { ValidationTheater } from '@/components/gate/ValidationTheater';
import { ApprovalsPanel } from '@/components/gate/ApprovalsPanel';
import { VerdictCard } from '@/components/gate/VerdictCard';
import { Panel, PanelHeader, PanelBody } from '@/components/ui/Panel';
import { Badge } from '@/components/ui/Badge';
import type { GateSpec } from '@/components/gate/VerdictCard';

// ---- Backend → view-model mapping -------------------------------------------
// The backend GateSpec projection (rowToGateSpec) returns the real walk-forward
// pass/fail pattern as a boolean[] under `windows`, plus pf/cons/robust/verdict
// from the validation verdict. Everything here is backend-sourced.
interface BackendSpec {
  id?: string;
  author?: string;
  author_id?: string;
  user_id?: string;
  kind?: string;
  regime?: string;
  pf?: number;
  cons?: number;
  robust?: number;
  windows?: boolean[];
  verdict?: string;
}

interface SpecsResponse {
  specs?: BackendSpec[];
}

function toGateSpec(d: BackendSpec): GateSpec {
  return {
    id: d.id ?? '',
    author: d.author ?? '',
    authorId: d.author_id ?? '',
    userId: d.user_id ?? '',
    kind: (d.kind ?? '') as GateSpec['kind'],
    regime: d.regime ?? 'UNKNOWN',
    pf: d.pf ?? 0,
    cons: d.cons ?? 0,
    robust: d.robust ?? 0,
    windows: Array.isArray(d.windows) ? d.windows : [],
    verdict: (d.verdict ?? 'queued') as GateSpec['verdict'],
    // A spec is "flashy" (overfit candidate) when PF clears the bar but
    // consistency does not — derived from real metrics, not hardcoded.
    flashy: (d.pf ?? 0) >= 1.3 && (d.cons ?? 0) < 60 && d.verdict === 'rejected',
  };
}

async function fetchSpecs(url: string): Promise<GateSpec[]> {
  const res = await apiFetch<SpecsResponse | BackendSpec[]>(url);
  const rows = Array.isArray(res) ? res : (res?.specs ?? []);
  return rows.map(toGateSpec);
}

// ---- Page -------------------------------------------------------------------
export default function GatePage() {
  const t = useTranslations('Gate');
  const {
    data: specs,
    error,
    isLoading,
    mutate,
  } = useSWR(
    '/api/v1/specs?status=queued,validating,certified,rejected',
    fetchSpecs,
    { refreshInterval: 15_000 },
  );

  const [selectedId, setSelectedId] = useState<string>('');
  const [revealed, setRevealed] = useState(true);

  const list = specs ?? [];
  const selectedSpec =
    list.find((s) => s.id === selectedId) ?? list[0] ?? null;

  function handleSelectSpec(spec: GateSpec) {
    setSelectedId(spec.id);
    setRevealed(true);
  }

  function handleValidate(updatedSpec: GateSpec) {
    setRevealed(true);
    void updatedSpec; // spec update from SSE would mutate SWR cache in a real integration
  }

  return (
    <div className="content-pad enter">
      <div className="maxw">
        {/* Page header */}
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            marginBottom: 16,
            flexWrap: 'wrap',
            gap: 10,
          }}
        >
          <div>
            <div className="eyebrow" style={{ color: 'var(--up)' }}>
              {t('eyebrow')}
            </div>
            <h2
              style={{ margin: '4px 0 0', fontSize: 17, color: 'var(--tx-hi)' }}
            >
              {t('heading')}
            </h2>
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <Badge variant="neutral">{t('badge_pf')}</Badge>
            <Badge variant="neutral">{t('badge_windows')}</Badge>
            <Badge variant="ok">{t('badge_cost')}</Badge>
          </div>
        </div>

        {/* ERROR STATE — backend no disponible, retry, sin datos falsos */}
        {error && (
          <Panel>
            <PanelHeader
              title={t('error_title')}
              sub={t('error_sub')}
            />
            <PanelBody>
              <p
                style={{
                  fontSize: 12,
                  color: 'var(--tx-dim)',
                  lineHeight: 1.55,
                  margin: '0 0 14px',
                }}
              >
                {t('error_body')}
              </p>
              <button className="btn primary sm" onClick={() => mutate()}>
                {t('retry')}
              </button>
            </PanelBody>
          </Panel>
        )}

        {/* LOADING STATE */}
        {!error && isLoading && (
          <Panel>
            <PanelBody>
              <p
                className="mono"
                style={{ fontSize: 11.5, color: 'var(--tx-faint)', margin: 0 }}
              >
                {t('loading')}
              </p>
            </PanelBody>
          </Panel>
        )}

        {/* MAIN — only when data loaded without error */}
        {!error && !isLoading && (
          <div
            style={{
              display: 'grid',
              gridTemplateColumns: '230px minmax(0,1fr) 280px',
              gap: 16,
              alignItems: 'start',
            }}
          >
            {/* LEFT — Cola de specs */}
            <GateQueue
              specs={list}
              selectedId={selectedSpec?.id ?? null}
              onSelect={handleSelectSpec}
            />

            {/* CENTER — Teatro de validación */}
            {selectedSpec ? (
              <ValidationTheater
                spec={selectedSpec}
                onValidate={(updated) => {
                  handleValidate(updated);
                  setRevealed(true);
                }}
              />
            ) : (
              <Panel>
                <PanelBody>
                  <p
                    style={{
                      fontSize: 12,
                      color: 'var(--tx-faint)',
                      margin: 0,
                    }}
                  >
                    {t('empty')}
                  </p>
                </PanelBody>
              </Panel>
            )}

            {/* RIGHT — Veredicto + nota filosófica */}
            <div
              style={{ display: 'flex', flexDirection: 'column', gap: 16 }}
            >
              {selectedSpec && (
                <VerdictCard spec={selectedSpec} revealed={revealed} />
              )}

              {/* Nota del gate — anti-overfitting */}
              <Panel>
                <PanelHeader
                  title={t('note_title')}
                  sub={t('note_sub')}
                />
                <PanelBody>
                  <p
                    style={{
                      fontSize: 11.5,
                      color: 'var(--tx-dim)',
                      lineHeight: 1.55,
                      margin: 0,
                    }}
                  >
                    {t('note_prefix')}{' '}
                    <b className="up">{t('note_stable')}</b>{' '}
                    {t('note_over')}{' '}
                    <b style={{ color: 'var(--micro)' }}>
                      {t('note_flashy')}
                    </b>
                    {t('note_suffix')}{' '}
                    <b className="up">{t('note_fees')}</b> {t('note_decide')}
                  </p>
                </PanelBody>
              </Panel>
            </div>
          </div>
        )}

        {/* Approvals — specs in shadow awaiting an operator's promote decision */}
        <div style={{ marginTop: 16 }}>
          <ApprovalsPanel />
        </div>
      </div>
    </div>
  );
}
