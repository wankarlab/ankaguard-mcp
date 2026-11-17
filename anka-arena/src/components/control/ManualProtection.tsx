'use client';

// ManualProtection — governs whether the bot adopts and manages the operator's
// MANUAL positions (placing SL/trailing/exit on them). This is a real-money
// safety control: turning it ON delegates close decisions to the bot. The master
// switch maps to the bool param bot.adopt.all_pairs.enabled.

import { useState } from 'react';
import useSWR, { useSWRConfig } from 'swr';
import { useTranslations } from 'next-intl';
import { Badge } from '@/components/ui/Badge';
import { ConfirmInline } from '@/components/ui/ConfirmInline';
import { ApiError } from '@/lib/api';
import {
  getManualProtection,
  setManualProtection,
  getParams,
  updateParam,
  getBotPositions,
  type Param,
  type BotPosition,
} from '@/lib/config-api';

function errText(e: unknown, t: ReturnType<typeof useTranslations>): string {
  if (e instanceof ApiError) {
    if (e.status === 403) return `403 · ${t('err_403')}`;
    if (e.status === 401) return `401 · ${t('err_401')}`;
    const body = e.body as { error?: string; message?: string } | undefined;
    const detail = body?.message || body?.error || '';
    return `${e.status}${detail ? ` · ${detail}` : ''}`;
  }
  return t('save_error');
}

// Advanced adoption knobs revealed only when protection is ON.
const ADVANCED_KEYS = [
  'bot.adopt.max_notional_usd',
  'bot.adopt.max_size_at_leverage1',
];

interface Props {
  canWrite: boolean;
}

export function ManualProtection({ canWrite }: Props) {
  const t = useTranslations('Control');
  const tc = useTranslations('Confirm');

  const { data: enabled, isLoading, mutate } = useSWR<boolean>(
    'config:manual-protection',
    getManualProtection,
    { revalidateOnFocus: false, refreshInterval: 20_000 },
  );

  const { data: positions, mutate: mutatePositions } = useSWR<BotPosition[]>(
    'bot:positions',
    getBotPositions,
    { revalidateOnFocus: false, refreshInterval: 30_000 },
  );

  const { mutate: globalMutate } = useSWRConfig();
  const [busy, setBusy] = useState(false);
  const [confirmOn, setConfirmOn] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  // Re-fetch every data source this panel renders. Toggling adoption changes the
  // backend-derived adoption columns on the positions table (adopted / source /
  // has_exchange_sl all come from bot.adopt.all_pairs.enabled) and reveals/hides
  // the advanced adoption params, so all three SWR caches must revalidate — not
  // just the switch state.
  async function refreshAll() {
    await Promise.all([
      mutate(),
      mutatePositions(),
      globalMutate('config:params:adopt-advanced'),
    ]);
  }

  async function toggle(next: boolean) {
    if (!canWrite || busy) return;
    setBusy(true);
    setMsg(null);
    // No optimistic update: this controls real-money behavior, so the view only
    // changes after the server confirms (refreshAll revalidates from the backend).
    try {
      await setManualProtection(next);
      setMsg({ kind: 'ok', text: next ? t('protection_on') : t('protection_off') });
      await refreshAll();
    } catch (e) {
      setMsg({ kind: 'err', text: errText(e, t) });
    } finally {
      setBusy(false);
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {/* Master toggle */}
      <div
        style={{
          padding: 14, borderRadius: 'var(--r)',
          border: `1px solid ${enabled ? 'var(--down)' : 'var(--line)'}`,
          background: enabled ? 'rgba(233,90,90,.05)' : 'var(--bg-inset)',
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12, flexWrap: 'wrap' }}>
          <div>
            <div style={{ fontSize: 14, fontWeight: 600, color: 'var(--tx-hi)' }}>{t('protect_title')}</div>
            <div style={{ fontSize: 11, color: 'var(--tx-dim)', marginTop: 2 }}>
              {isLoading ? t('loading') : enabled ? t('protect_state_on') : t('protect_state_off')}
            </div>
          </div>
          <button
            type="button"
            role="switch"
            aria-checked={!!enabled}
            disabled={!canWrite || busy || isLoading}
            onClick={() => {
              // Enabling delegates close decisions to the bot: confirm first.
              if (!enabled) setConfirmOn(true);
              else toggle(false);
            }}
            style={{
              width: 56, height: 30, borderRadius: 16, position: 'relative',
              border: '1px solid var(--line)', cursor: canWrite && !busy ? 'pointer' : 'not-allowed',
              background: enabled ? 'var(--down)' : 'var(--bg-void)', transition: 'background .15s',
            }}
          >
            <span
              style={{
                position: 'absolute', top: 3, left: enabled ? 28 : 3,
                width: 22, height: 22, borderRadius: '50%',
                background: 'var(--tx-hi)', transition: 'left .15s',
              }}
            />
          </button>
        </div>

        {confirmOn && !enabled && (
          <div style={{ marginTop: 12 }}>
            <ConfirmInline
              message={tc('adopt_on')}
              confirmLabel={tc('adopt_on_btn')}
              cancelLabel={tc('cancel')}
              busy={busy}
              onCancel={() => setConfirmOn(false)}
              onConfirm={async () => {
                setConfirmOn(false);
                await toggle(true);
              }}
            />
          </div>
        )}

        {/* Always-visible warning */}
        <div
          style={{
            marginTop: 12, padding: '10px 12px', borderRadius: 6,
            border: '1px solid var(--down)', background: 'rgba(233,90,90,.07)',
            color: 'var(--down)', fontSize: 12, lineHeight: 1.5,
          }}
        >
          {t('protect_warning')}
        </div>
      </div>

      {/* Advanced params (only when ON) */}
      {enabled && (
        <div>
          <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', textTransform: 'uppercase', letterSpacing: '.06em', marginBottom: 8 }}>
            {t('advanced_label')}
          </div>
          <AdvancedAdoptParams canWrite={canWrite} />
        </div>
      )}

      {/* Positions table */}
      <div>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 8 }}>
          <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', textTransform: 'uppercase', letterSpacing: '.06em' }}>
            {t('positions_label')}
          </div>
          <button type="button" onClick={() => mutatePositions()} style={{ background: 'none', border: 'none', color: 'var(--tx-dim)', fontSize: 11, cursor: 'pointer' }}>
            {t('refresh')}
          </button>
        </div>
        <PositionsTable positions={positions ?? []} />
      </div>

      {msg && (
        <div
          className="mono"
          style={{
            fontSize: 11, padding: '8px 12px', borderRadius: 'var(--r)',
            color: msg.kind === 'ok' ? 'var(--up)' : 'var(--down)',
            background: msg.kind === 'ok' ? 'rgba(74,219,132,.07)' : 'rgba(233,90,90,.07)',
          }}
        >
          {msg.text}
        </div>
      )}
    </div>
  );
}

// ── Advanced adoption sliders ─────────────────────────────────────────────────

function AdvancedAdoptParams({ canWrite }: { canWrite: boolean }) {
  const t = useTranslations('Control');
  const { data, mutate } = useSWR<Param[]>(
    'config:params:adopt-advanced',
    async () => {
      const all = await getParams({ group: 'bot' });
      return all.filter((p) => ADVANCED_KEYS.includes(p.key));
    },
    { revalidateOnFocus: false },
  );

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      {(data ?? []).map((p) => (
        <AdoptSlider key={p.key} param={p} canWrite={canWrite} onSaved={() => mutate()} />
      ))}
      {(data?.length ?? 0) === 0 && (
        <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)' }}>{t('no_advanced')}</div>
      )}
    </div>
  );
}

function AdoptSlider({ param, canWrite, onSaved }: { param: Param; canWrite: boolean; onSaved: () => void }) {
  const t = useTranslations('Control');
  const effective = param.current_effective_value ?? param.default ?? '0';
  const [val, setVal] = useState(effective);
  const [busy, setBusy] = useState(false);
  const dirty = val !== effective;
  const min = param.min ?? 0;
  const max = param.max ?? 1000;
  const step = param.step ?? 1;

  async function commit() {
    if (!canWrite || !dirty || busy) return;
    setBusy(true);
    try {
      await updateParam(param.key, val, 'adopt advanced tuning');
      onSaved();
    } finally {
      setBusy(false);
    }
  }

  return (
    <div style={{ display: 'grid', gridTemplateColumns: '1fr auto', gap: 10, alignItems: 'center', padding: '8px 10px', borderRadius: 6, border: '1px solid var(--line)' }}>
      <div>
        <div className="mono" style={{ fontSize: 11, color: 'var(--tx-hi)' }}>{param.key}</div>
        {param.description && <div style={{ fontSize: 10, color: 'var(--tx-dim)', marginTop: 2 }}>{param.description}</div>}
        <input
          type="range" min={min} max={max} step={step}
          value={parseFloat(val) || 0} disabled={!canWrite}
          onChange={(e) => setVal(e.target.value)}
          style={{ width: '100%', accentColor: 'var(--brand)', marginTop: 6 }}
        />
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: 4 }}>
        <input
          type="number" min={min} max={max} step={step}
          value={val} disabled={!canWrite}
          onChange={(e) => setVal(e.target.value)}
          style={{ width: 100, padding: '5px 8px', borderRadius: 4, border: '1px solid var(--line)', background: 'var(--bg-void)', color: 'var(--tx-hi)', fontSize: 12 }}
        />
        {dirty && (
          <button type="button" disabled={!canWrite || busy} onClick={commit} style={{ padding: '4px 10px', borderRadius: 4, border: 'none', background: 'var(--brand)', color: 'var(--bg)', fontSize: 11, fontWeight: 600, cursor: 'pointer' }}>
            {t('apply')}
          </button>
        )}
      </div>
    </div>
  );
}

// ── Positions table ───────────────────────────────────────────────────────────

function PositionsTable({ positions }: { positions: BotPosition[] }) {
  const t = useTranslations('Control');

  if (positions.length === 0) {
    return <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)', padding: 12 }}>{t('no_positions')}</div>;
  }

  const th: React.CSSProperties = { textAlign: 'left', padding: '6px 8px', fontSize: 10, color: 'var(--tx-faint)', textTransform: 'uppercase', letterSpacing: '.04em', borderBottom: '1px solid var(--line)' };
  const td: React.CSSProperties = { padding: '8px', fontSize: 12, color: 'var(--tx-hi)', borderBottom: '1px solid var(--line)' };

  return (
    <div style={{ overflowX: 'auto' }}>
      <table className="mono" style={{ width: '100%', borderCollapse: 'collapse' }}>
        <thead>
          <tr>
            <th style={th}>{t('col_symbol')}</th>
            <th style={th}>{t('col_side')}</th>
            <th style={th}>{t('col_size')}</th>
            <th style={th}>{t('col_pnl')}</th>
            <th style={th}>{t('col_protected')}</th>
            <th style={th}>{t('col_exchange_sl')}</th>
            <th style={th}>{t('col_source')}</th>
          </tr>
        </thead>
        <tbody>
          {positions.map((p, i) => (
            <tr key={`${p.symbol}-${i}`}>
              <td style={td}>{p.symbol}</td>
              <td style={{ ...td, color: /short|sell/i.test(p.side) ? 'var(--down)' : 'var(--up)' }}>{p.side}</td>
              <td style={td}>{p.size}</td>
              <td style={{ ...td, color: (p.pnl ?? 0) >= 0 ? 'var(--up)' : 'var(--down)' }}>
                {(p.pnl ?? 0) >= 0 ? '+' : ''}{(p.pnl ?? 0).toFixed(2)}
                {typeof p.pnl_pct === 'number' && ` (${p.pnl_pct >= 0 ? '+' : ''}${p.pnl_pct.toFixed(2)}%)`}
              </td>
              <td style={td}>{p.adopted ? '✅' : '⚠️'}</td>
              <td style={td}>{p.has_exchange_sl ? '✅' : '❌'}</td>
              <td style={td}>
                <Badge variant={p.source === 'bot' ? 'brand' : 'neutral'}>{p.source}</Badge>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
