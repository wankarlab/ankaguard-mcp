'use client';

// SizingSection — edits the capital-relative sizing keys (trade size, daily
// loss cap, per-pair pause, drawdown, AI-02 limits, sentinel caps, tier
// ladder) and previews their USD effect against the bot's live capital
// before saving. Writes go through one atomic POST /params/bulk with a reason.

import { useEffect, useMemo, useState } from 'react';
import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { Panel, PanelHeader, PanelBody } from '@/components/ui/Panel';
import { Badge } from '@/components/ui/Badge';
import { ApiError } from '@/lib/api';
import { isProductEdition } from '@/lib/productMode';
import type { ParamItem } from '@/lib/scalping';
import {
  SIZING_GROUPS,
  TIERS_KEY,
  fetchSizingParams,
  fetchBotSizing,
  saveSizing,
  parseTiers,
  previewSizing,
  type SizingField,
  type SizingTier,
} from '@/lib/sizing';

const usd = (n: number) => `${n.toFixed(2)} USD`;

function errText(e: unknown, t: (k: string) => string): string {
  if (e instanceof ApiError) {
    const body = e.body as { error?: string; message?: string } | undefined;
    const detail = body?.message || body?.error || '';
    if (e.status === 403) return `403 · ${t('err_403')}`;
    if (e.status === 401) return `401 · ${t('err_401')}`;
    return `${e.status}${detail ? ` · ${detail}` : ''}`;
  }
  return t('save_error');
}

export function SizingSection({ canWrite }: { canWrite: boolean }) {
  const t = useTranslations('Sizing');

  const params = useSWR('sizing:params', fetchSizingParams, { revalidateOnFocus: false, refreshInterval: 30_000 });
  // The product runs no automatic bot (its /ai-bot routes do not exist), so
  // there is no live bot state to read there.
  const product = isProductEdition();
  const bot = useSWR(product ? null : 'sizing:bot', fetchBotSizing, { revalidateOnFocus: false, refreshInterval: 15_000 });

  const [edits, setEdits] = useState<Record<string, string>>({});
  const [tierEdits, setTierEdits] = useState<Record<number, string>>({});
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  // The preview bar sticks to the bottom only on wide screens; on a phone it
  // would cover most of the form.
  const [wide, setWide] = useState(false);
  useEffect(() => {
    const mq = window.matchMedia('(min-width: 900px)');
    const sync = () => setWide(mq.matches);
    sync();
    mq.addEventListener('change', sync);
    return () => mq.removeEventListener('change', sync);
  }, []);

  const p: Record<string, ParamItem> = useMemo(() => params.data ?? {}, [params.data]);
  const stored = (key: string) => p[key]?.current_effective_value ?? p[key]?.default ?? '';
  const value = (key: string) => (key in edits ? edits[key] : stored(key));

  const baseTiers = useMemo(() => parseTiers(stored(TIERS_KEY)), [p]); // eslint-disable-line react-hooks/exhaustive-deps
  const tiers: SizingTier[] = useMemo(
    () => baseTiers.map((tr, i) => (i in tierEdits ? { ...tr, size_pct: parseFloat(tierEdits[i]) || 0 } : tr)),
    [baseTiers, tierEdits],
  );

  const preview = useMemo(() => previewSizing(value, tiers, bot.data), [edits, tiers, bot.data, p]); // eslint-disable-line react-hooks/exhaustive-deps

  const tiersDirty = Object.keys(tierEdits).length > 0;
  const dirtyCount = Object.keys(edits).length + (tiersDirty ? 1 : 0);
  const reasonOk = reason.trim().length >= 6;

  function edit(key: string, v: string) {
    setMsg(null);
    setEdits((prev) => {
      const next = { ...prev };
      if (v === stored(key)) delete next[key];
      else next[key] = v;
      return next;
    });
  }

  function editTier(i: number, v: string) {
    setMsg(null);
    setTierEdits((prev) => {
      const next = { ...prev };
      if (parseFloat(v) === (baseTiers[i]?.size_pct ?? 0)) delete next[i];
      else next[i] = v;
      return next;
    });
  }

  function discard() {
    setEdits({});
    setTierEdits({});
    setReason('');
  }

  async function save() {
    if (!canWrite || !reasonOk || dirtyCount === 0) return;
    setBusy(true);
    setMsg(null);
    try {
      const changes = { ...edits };
      if (tiersDirty) changes[TIERS_KEY] = JSON.stringify(tiers);
      await saveSizing(changes, reason.trim());
      discard();
      setMsg({ kind: 'ok', text: t('saved', { n: Object.keys(changes).length }) });
      await Promise.all([params.mutate(), bot.mutate()]);
    } catch (e) {
      setMsg({ kind: 'err', text: errText(e, t) });
    } finally {
      setBusy(false);
    }
  }

  if (params.isLoading) {
    return <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)', padding: 16 }}>{t('loading')}</div>;
  }
  if (params.error) {
    return <div className="mono" style={{ fontSize: 12, color: 'var(--down)', padding: 16 }}>{t('load_error')}</div>;
  }

  const live = bot.data?.sizing;
  const isAbsolute = value('bot.sizing.mode') === 'absolute';

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {/* ── What the bot trades with right now ─────────────────────────── */}
      {product ? (
        <Panel>
          <PanelHeader title={t('product_live_title')} />
          <PanelBody>
            <div style={{ fontSize: 12, color: 'var(--tx)' }}>{t('product_live_text')}</div>
          </PanelBody>
        </Panel>
      ) : (
      <Panel>
        <PanelHeader title={t('live_title')} sub={t('live_sub')} />
        <PanelBody>
          {!live ? (
            <div className="mono" style={{ fontSize: 12, color: 'var(--tx-dim)' }}>
              {bot.error ? t('live_error') : t('live_none')}
            </div>
          ) : (
            <>
              <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginBottom: 12 }}>
                <Badge variant={bot.data?.running ? 'ok' : 'micro'} dot>
                  {bot.data?.running ? t('bot_running') : t('bot_stopped')}
                </Badge>
                <Badge variant="brand">{live.mode === 'absolute' ? t('mode_absolute') : t('mode_capital_pct')}</Badge>
                {live.fallback && <Badge variant="micro">{t('fallback')}</Badge>}
                {live.drawdown_size_factor < 1 && (
                  <Badge variant="micro">{t('dd_active', { f: live.drawdown_size_factor })}</Badge>
                )}
              </div>
              <div style={statGrid}>
                <Stat label={t('s_capital')} value={usd(live.capital_usd)} />
                <Stat label={t('s_day_start')} value={live.day_start_capital_usd ? usd(live.day_start_capital_usd) : '—'} />
                <Stat label={t('s_base')} value={usd(live.base_notional_usd)} />
                <Stat
                  label={t('s_daily')}
                  value={`${usd(live.daily_loss_usd)} / ${usd(live.daily_loss_limit_usd)}`}
                  tone={live.daily_loss_limit_usd > 0 && live.daily_loss_usd >= live.daily_loss_limit_usd ? 'down' : undefined}
                />
                <Stat label={t('s_pair_pause')} value={usd(live.pair_pause_threshold_usd)} />
                <Stat label={t('s_bounds')} value={`${live.min_notional_usd} – ${live.max_notional_usd} USD`} />
              </div>
            </>
          )}
        </PanelBody>
      </Panel>
      )}

      {/* ── Editable groups ─────────────────────────────────────────────── */}
      {SIZING_GROUPS.map((g) => (
        <Panel key={g.id}>
          <PanelHeader title={t(g.titleKey)} sub={t(g.subKey)} />
          <PanelBody>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
              {g.fields.map((f) => (
                <FieldRow
                  key={f.key}
                  field={f}
                  value={value(f.key)}
                  meta={p[f.key]}
                  dirty={f.key in edits}
                  disabled={!canWrite || busy}
                  label={t(f.labelKey)}
                  hint={t(f.hintKey)}
                  effect={fieldEffect(f.key, preview, isAbsolute, t)}
                  onChange={(v) => edit(f.key, v)}
                />
              ))}
            </div>
          </PanelBody>
        </Panel>
      ))}

      {/* ── Tier ladder ─────────────────────────────────────────────────── */}
      <Panel>
        <PanelHeader title={t('g_tiers')} sub={t('g_tiers_sub')} />
        <PanelBody>
          {tiers.length === 0 ? (
            <div className="mono" style={{ fontSize: 12, color: 'var(--tx-dim)' }}>{t('tiers_none')}</div>
          ) : (
            <div style={{ overflowX: 'auto' }}>
              <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
                <thead>
                  <tr style={{ color: 'var(--tx-faint)', textAlign: 'left' }}>
                    <th style={th}>{t('t_name')}</th>
                    <th style={th}>{t('t_conf')}</th>
                    <th style={th}>{t('t_pct')}</th>
                    <th style={th}>{t('t_usd_now')}</th>
                    <th style={th}>{t('t_usd_fallback')}</th>
                  </tr>
                </thead>
                <tbody>
                  {tiers.map((tr, i) => {
                    const row = preview?.tiers[i];
                    return (
                      <tr key={tr.name + i} style={{ background: i in tierEdits ? 'rgba(70,200,216,.08)' : 'transparent' }}>
                        <td style={td}>{tr.name}</td>
                        <td style={td} className="mono">{row?.range ?? `${tr.min_confidence}–${tr.max_confidence}`}</td>
                        <td style={td}>
                          <input
                            type="number" min={0.1} max={100} step={0.5}
                            value={i in tierEdits ? tierEdits[i] : String(tr.size_pct ?? '')}
                            disabled={!canWrite || busy || isAbsolute} suppressHydrationWarning
                            onChange={(e) => editTier(i, e.target.value)}
                            style={{ ...inputStyle, width: 80 }}
                          />{' '}%
                        </td>
                        <td style={td} className="mono">{row ? usd(row.usd) : '—'}</td>
                        <td style={{ ...td, color: 'var(--tx-faint)' }} className="mono">
                          {tr.size_usd != null ? usd(tr.size_usd) : '—'}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
              {isAbsolute && (
                <div className="mono" style={{ fontSize: 11, color: 'var(--tx-dim)', marginTop: 8 }}>{t('tiers_absolute')}</div>
              )}
            </div>
          )}
        </PanelBody>
      </Panel>

      {/* ── Preview + commit bar ────────────────────────────────────────── */}
      <div
        style={{
          position: wide ? 'sticky' : 'static', bottom: 'calc(env(safe-area-inset-bottom, 0px) + 8px)',
          padding: 14, borderRadius: 'var(--r)',
          border: `1px solid ${dirtyCount > 0 ? 'var(--brand)' : 'var(--line)'}`,
          background: 'var(--bg-inset)',
        }}
      >
        <div style={{ fontSize: 12, color: 'var(--tx-hi)', fontWeight: 600, marginBottom: 8 }}>
          {dirtyCount > 0 ? t('preview_pending', { n: dirtyCount }) : t('preview_current')}
        </div>
        {preview ? (
          <div style={statGrid}>
            <Stat label={t('p_base')} value={usd(preview.base)} />
            <Stat label={t('p_max')} value={usd(preview.botMax)} />
            <Stat label={t('p_daily')} value={usd(preview.dailyCap)} />
            <Stat label={t('p_pair_pause')} value={usd(preview.pairPause)} />
            <Stat label={t('p_sentinel')} value={preview.sentinelCap > 0 ? usd(preview.sentinelCap) : t('none')} />
            <Stat label={t('p_risk')} value={`${preview.baseRiskPct.toFixed(2)} %`} />
          </div>
        ) : (
          <div className="mono" style={{ fontSize: 11, color: 'var(--tx-dim)' }}>{t('preview_no_capital')}</div>
        )}
        {preview && (preview.incoherent || preview.floorApplied || preview.ceilingApplied) && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 4, marginTop: 10 }}>
            {preview.incoherent && (
              <Warn text={t('w_incoherent', { cap: preview.sentinelCap.toFixed(2), max: preview.botMax.toFixed(2) })} />
            )}
            {preview.floorApplied && <Warn text={t('w_floor')} />}
            {preview.ceilingApplied && <Warn text={t('w_ceiling')} />}
          </div>
        )}

        {dirtyCount > 0 && (
          <div style={{ marginTop: 12 }}>
            <input
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              placeholder={t('reason_placeholder')}
              disabled={!canWrite || busy} suppressHydrationWarning
              style={{ ...inputStyle, width: '100%', marginBottom: 8 }}
            />
            <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
              <button
                type="button"
                disabled={!canWrite || !reasonOk || busy} suppressHydrationWarning
                onClick={save}
                style={{
                  padding: '7px 14px', borderRadius: 4, border: 'none',
                  background: reasonOk && canWrite ? 'var(--brand)' : 'var(--line)',
                  color: 'var(--bg)', fontWeight: 600, fontSize: 12,
                  cursor: reasonOk && canWrite && !busy ? 'pointer' : 'not-allowed',
                }}
              >
                {busy ? t('saving') : t('apply', { n: dirtyCount })}
              </button>
              <button
                type="button" disabled={busy} suppressHydrationWarning onClick={discard}
                style={{
                  padding: '7px 14px', borderRadius: 4, border: '1px solid var(--line)',
                  background: 'transparent', color: 'var(--tx-dim)', fontSize: 12, cursor: 'pointer',
                }}
              >
                {t('discard')}
              </button>
              {!reasonOk && (
                <span className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)', alignSelf: 'center' }}>
                  {t('reason_hint')}
                </span>
              )}
            </div>
          </div>
        )}

        {msg && (
          <div
            className="mono"
            style={{
              marginTop: 10, fontSize: 11, padding: '8px 12px', borderRadius: 'var(--r)',
              color: msg.kind === 'ok' ? 'var(--up)' : 'var(--down)',
              background: msg.kind === 'ok' ? 'rgba(74,219,132,.07)' : 'rgba(233,90,90,.07)',
            }}
          >
            {msg.text}
          </div>
        )}
      </div>
    </div>
  );
}

// The USD consequence of a field, shown under its input.
function fieldEffect(
  key: string,
  pv: ReturnType<typeof previewSizing>,
  isAbsolute: boolean,
  t: (k: string, v?: Record<string, string | number>) => string,
): string | null {
  if (!pv) return null;
  switch (key) {
    case 'bot.sizing.base_pct':
      return isAbsolute ? t('e_absolute') : t('e_base', { usd: pv.base.toFixed(2) });
    case 'bot.max_daily_loss_percent':
    case 'bot.max_daily_loss_usd':
      return t('e_daily', { usd: pv.dailyCap.toFixed(2) });
    case 'bot.pair_pause.net_loss_pct':
      return t('e_pair_pause', { usd: pv.pairPause.toFixed(2) });
    case 'risk.max_order_size_usd':
    case 'risk.max_order_size_pct':
      return pv.sentinelCap > 0 ? t('e_sentinel', { usd: pv.sentinelCap.toFixed(2) }) : t('none');
    default:
      return null;
  }
}

interface FieldRowProps {
  field: SizingField;
  value: string;
  meta?: ParamItem;
  dirty: boolean;
  disabled: boolean;
  label: string;
  hint: string;
  effect: string | null;
  onChange: (v: string) => void;
}

function FieldRow({ field, value, meta, dirty, disabled, label, hint, effect, onChange }: FieldRowProps) {
  const isDefault = meta ? value === meta.default : false;
  return (
    <div
      style={{
        display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 220px), 1fr))', gap: 8,
        alignItems: 'center', padding: '9px 10px', borderRadius: 6,
        background: dirty ? 'rgba(70,200,216,.08)' : 'transparent',
      }}
    >
      <div style={{ minWidth: 0 }}>
        <div style={{ fontSize: 12, color: 'var(--tx-hi)' }}>{label}</div>
        <div style={{ fontSize: 11, color: 'var(--tx-dim)', marginTop: 2 }}>{hint}</div>
        <div className="mono" style={{ fontSize: 9, color: 'var(--tx-faint)', marginTop: 2 }}>
          {field.key}{meta && !isDefault ? ` · default ${meta.default}` : ''}
        </div>
      </div>
      <div>
        {field.type === 'bool' ? (
          <select value={value} disabled={disabled} suppressHydrationWarning onChange={(e) => onChange(e.target.value)} style={inputStyle}>
            <option value="true">ON</option>
            <option value="false">OFF</option>
          </select>
        ) : field.type === 'enum' ? (
          <select value={value} disabled={disabled} suppressHydrationWarning onChange={(e) => onChange(e.target.value)} style={inputStyle}>
            {field.enum!.map((o) => <option key={o} value={o}>{o}</option>)}
          </select>
        ) : (
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
            <input
              type="number" min={field.min} max={field.max} step={field.step}
              value={value} disabled={disabled} suppressHydrationWarning
              onChange={(e) => onChange(e.target.value)}
              style={{ ...inputStyle, width: 110 }}
            />
            {field.unit && <span className="mono" style={{ fontSize: 11, color: 'var(--tx-dim)' }}>{field.unit}</span>}
          </span>
        )}
        {effect && (
          <div className="mono" style={{ fontSize: 11, color: 'var(--brand)', marginTop: 4 }}>{effect}</div>
        )}
      </div>
    </div>
  );
}

function Stat({ label, value, tone }: { label: string; value: string; tone?: 'down' }) {
  return (
    <div style={{ padding: '8px 10px', borderRadius: 6, border: '1px solid var(--line)', background: 'var(--bg-void)' }}>
      <div style={{ fontSize: 10, color: 'var(--tx-faint)', textTransform: 'uppercase', letterSpacing: '.05em' }}>{label}</div>
      <div className="mono" style={{ fontSize: 13, color: tone === 'down' ? 'var(--down)' : 'var(--tx-hi)', marginTop: 2 }}>{value}</div>
    </div>
  );
}

function Warn({ text }: { text: string }) {
  return (
    <div className="mono" style={{ fontSize: 11, color: 'var(--down)', padding: '6px 10px', borderRadius: 4, background: 'rgba(233,90,90,.07)' }}>
      {text}
    </div>
  );
}

const statGrid: React.CSSProperties = {
  display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(150px, 1fr))', gap: 8,
};
const inputStyle: React.CSSProperties = {
  padding: '6px 8px', borderRadius: 4, border: '1px solid var(--line)',
  background: 'var(--bg-void)', color: 'var(--tx-hi)', fontSize: 12,
};
const th: React.CSSProperties = { padding: '6px 8px', fontWeight: 500, fontSize: 11 };
const td: React.CSSProperties = { padding: '6px 8px', borderTop: '1px solid var(--line)', color: 'var(--tx-hi)' };
