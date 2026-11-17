'use client';

import { useEffect, useMemo, useState } from 'react';
import useSWR from 'swr';
import { useTranslations } from 'next-intl';
import { Badge } from '@/components/ui/Badge';
import { ApiError } from '@/lib/api';
import { fetchGateParams, setKnob } from '@/lib/scalping';

// Live-bot operation bar. These controls drive the LIVE singleton bot (the one
// that actually trades, using the global .env keys) through the gate — NOT the
// per-user BotManager bot (which needs the caller's own BYO-keys and is a
// separate process).
//
// Pause/Resume/KILL toggle ONLY the master validation gate
// (bot.validation_gate.enabled): ON blocks every new entry, OFF lets entries
// flow per the operator's regime/confidence tuning. They deliberately do NOT
// apply a full preset — an earlier version resumed via the Conservative preset,
// which silently reset bot.entry.regime_gate.mode back to "trending" and
// clobbered the operator's "ranging" choice. Tuning the strategy itself stays in
// the presets / advanced sliders below; this bar only starts/stops trading.

const GATE_KEY = 'bot.validation_gate.enabled';

interface Props {
  canWrite: boolean;
}

export function BotStatusBar({ canWrite }: Props) {
  const t = useTranslations('Scalping');
  // Share the SWR cache key with ScalpingPanel so applying here refreshes both.
  const { data, isLoading, mutate } = useSWR('scalping:gate-params', fetchGateParams, {
    refreshInterval: 10_000,
    revalidateOnFocus: false,
  });

  const [busy, setBusy] = useState<null | 'resume' | 'pause' | 'kill'>(null);
  const [confirmKill, setConfirmKill] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  const [mounted, setMounted] = useState(false);
  useEffect(() => setMounted(true), []);

  const params = useMemo(() => data ?? {}, [data]);
  // "Paused" = the Flat state: the gate blocks ALL new entries. That's the Flat
  // preset's signature (validation gate on, or a confidence floor ≥0.85). The
  // regime heuristic is intentionally NOT used here — Conservative keeps regime
  // on 'trending' yet the bot still trades, so it must read as Operating.
  const paused = useMemo(() => {
    const v = (k: string) => params[k]?.current_effective_value;
    const conf = parseFloat(v('bot.confidence_threshold') ?? '0');
    return v('bot.validation_gate.enabled') === 'true' || conf >= 0.85;
  }, [params]);

  async function act(kind: 'resume' | 'pause' | 'kill') {
    if (!canWrite) return;
    setBusy(kind);
    setMsg(null);
    try {
      // resume → gate OFF (entries flow); pause/kill → gate ON (block all).
      const value = kind === 'resume' ? 'false' : 'true';
      await setKnob(GATE_KEY, value, `live-bot ${kind} via scalping panel`);
      setMsg({ kind: 'ok', text: t(`live_${kind}_ok`) });
      setConfirmKill(false);
      await mutate();
    } catch (e) {
      let text = t('bot_action_error');
      if (e instanceof ApiError) {
        const body = e.body as { message?: string; error?: string } | undefined;
        text = `${e.status} · ${body?.message || body?.error || t('bot_action_error')}`;
      }
      setMsg({ kind: 'err', text });
    } finally {
      setBusy(null);
    }
  }

  if (!mounted) {
    return (
      <div style={barStyle}>
        <span className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{t('loading')}</span>
      </div>
    );
  }

  return (
    <div style={{ ...barStyle, flexDirection: 'column', gap: 10 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
        {isLoading ? (
          <span className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{t('loading')}</span>
        ) : paused ? (
          <Badge variant="micro" dot>{t('live_paused')}</Badge>
        ) : (
          <Badge variant="ok" dot>{t('live_operating')}</Badge>
        )}

        <span className="mono" style={{ fontSize: 11, color: 'var(--tx-dim)', flex: 1 }}>
          {t('live_note')}
        </span>

        <div style={{ display: 'flex', gap: 8 }}>
          <button
            type="button"
            disabled={!canWrite || busy !== null || !paused} suppressHydrationWarning
            onClick={() => act('resume')}
            style={btnStyle('var(--up)', canWrite && paused && busy === null)}
          >
            ▶ {busy === 'resume' ? '…' : t('btn_resume')}
          </button>
          <button
            type="button"
            disabled={!canWrite || busy !== null || paused} suppressHydrationWarning
            onClick={() => act('pause')}
            style={btnStyle('var(--tx-dim)', canWrite && !paused && busy === null)}
          >
            ⏸ {busy === 'pause' ? '…' : t('btn_pause')}
          </button>
          <button
            type="button"
            disabled={!canWrite || busy !== null} suppressHydrationWarning
            onClick={() => setConfirmKill(true)}
            style={btnStyle('var(--down)', canWrite && busy === null)}
          >
            🛑 {t('btn_kill')}
          </button>
        </div>
      </div>

      {confirmKill && (
        <div style={{ padding: 10, borderRadius: 6, border: '1px solid var(--down)', background: 'rgba(233,90,90,.07)' }}>
          <div style={{ fontSize: 12, color: 'var(--tx-hi)', marginBottom: 8 }}>{t('kill_confirm')}</div>
          <div style={{ display: 'flex', gap: 8 }}>
            <button
              type="button" disabled={busy !== null} suppressHydrationWarning
              onClick={() => act('kill')}
              style={{ padding: '6px 14px', borderRadius: 4, border: 'none', background: 'var(--down)', color: 'var(--bg)', fontWeight: 600, fontSize: 12, cursor: busy ? 'wait' : 'pointer' }}
            >
              {busy === 'kill' ? '…' : t('kill_confirm_yes')}
            </button>
            <button
              type="button" disabled={busy !== null} suppressHydrationWarning
              onClick={() => setConfirmKill(false)}
              style={{ padding: '6px 14px', borderRadius: 4, border: '1px solid var(--line)', background: 'transparent', color: 'var(--tx-dim)', fontSize: 12, cursor: 'pointer' }}
            >
              {t('cancel')}
            </button>
          </div>
        </div>
      )}

      {msg && (
        <div className="mono" style={{ fontSize: 11, color: msg.kind === 'ok' ? 'var(--up)' : 'var(--down)' }}>
          {msg.text}
        </div>
      )}
    </div>
  );
}

const barStyle: React.CSSProperties = {
  display: 'flex', padding: '12px 14px', borderRadius: 'var(--r)',
  border: '1px solid var(--line)', background: 'var(--bg-inset)',
};

function btnStyle(color: string, enabled: boolean): React.CSSProperties {
  return {
    padding: '6px 12px', borderRadius: 4, fontSize: 12, whiteSpace: 'nowrap',
    border: `1px solid ${color}`, background: 'transparent', color,
    cursor: enabled ? 'pointer' : 'not-allowed', opacity: enabled ? 1 : 0.45,
  };
}
