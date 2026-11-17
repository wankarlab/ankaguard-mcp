'use client';

// Shared helpers and styles for the Launch screen components.

import { useEffect, useState, type CSSProperties } from 'react';
import { useTranslations } from 'next-intl';
import { usePermissionsStore } from '@/stores/permissions';
import { OPERATE_MIN_LEVEL, exitReasonKey, reasonKey, bandKey, screenKey, statusKey } from '@/lib/launch-api';

export type T = ReturnType<typeof useTranslations>;

export function fmtUsd(n: number | null | undefined, digits = 2): string {
  if (n == null || !isFinite(n)) return '—';
  const sign = n < 0 ? '-' : '';
  return `${sign}$${Math.abs(n).toLocaleString('en-US', { minimumFractionDigits: digits, maximumFractionDigits: digits })}`;
}

export function fmtNum(n: number | null | undefined, digits = 2): string {
  if (n == null || !isFinite(n)) return '—';
  return n.toLocaleString('en-US', { minimumFractionDigits: digits, maximumFractionDigits: digits });
}

export function fmtPct(n: number | null | undefined): string {
  if (n == null || !isFinite(n)) return '—';
  return `${(n * 100).toFixed(1)}%`;
}

export function fmtTime(iso: string | null | undefined): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (isNaN(d.getTime()) || d.getUTCFullYear() < 2000) return '—';
  return d.toLocaleString();
}

export function pnlColor(n: number | null | undefined): string {
  if (n == null || n === 0) return 'var(--tx-hi)';
  return n > 0 ? 'var(--up)' : 'var(--down)';
}

/** Translate a known code through a key builder, falling back to the raw code. */
export function tCode(t: T, key: string | null, raw: string | null | undefined): string {
  if (!raw) return '—';
  return key ? t(key as never) : raw;
}
export const tReason = (t: T, c: string | null | undefined) => tCode(t, reasonKey(c), c);
export const tExit = (t: T, c: string | null | undefined) => tCode(t, exitReasonKey(c), c);
export const tBand = (t: T, c: string | null | undefined) => tCode(t, bandKey(c), c);
export const tScreen = (t: T, c: string | null | undefined) => tCode(t, screenKey(c), c);
/** Decision category; "none" groups expired items without an operator category. */
export const tCat = (t: T, c: string | null | undefined) =>
  tCode(t, c && ['narrative', 'team', 'community', 'chart', 'other', 'none'].includes(c) ? `cat_${c}` : null, c);
export const tStatus = (t: T, c: string | null | undefined) => tCode(t, statusKey(c), c);

/** Wall clock that ticks every second (starts at 0 until mounted: hydration-safe). */
export function useNow(intervalMs = 1000): number {
  const [now, setNow] = useState(0);
  useEffect(() => {
    setNow(Date.now());
    const id = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(id);
  }, [intervalMs]);
  return now;
}

/**
 * Operate permission from the backend permissions store (user_level >= 3).
 * Forced to false until after mount so SSR and first client paint agree.
 */
export function useOperatePermission(): { ready: boolean; level: number; allowed: boolean } {
  const load = usePermissionsStore((s) => s.load);
  const loaded = usePermissionsStore((s) => s.loaded);
  const level = usePermissionsStore((s) => s.permissions?.userLevel ?? 0);
  useEffect(() => {
    load();
  }, [load]);
  const [mounted, setMounted] = useState(false);
  useEffect(() => {
    setMounted(true);
  }, []);
  const ready = mounted && loaded;
  return { ready, level, allowed: ready && level >= OPERATE_MIN_LEVEL };
}

export const thStyle: CSSProperties = { padding: '4px 8px', textAlign: 'left' };
export const thRight: CSSProperties = { padding: '4px 8px', textAlign: 'right' };
export const tdStyle: CSSProperties = { padding: '6px 8px', borderTop: '1px solid var(--line)' };
export const tdRight: CSSProperties = { ...tdStyle, textAlign: 'right' };
export const theadRow: CSSProperties = { color: 'var(--tx-faint)', fontSize: 10, textTransform: 'uppercase' };

export const inputStyle: CSSProperties = {
  padding: '6px 10px', borderRadius: 'var(--r)', border: '1px solid var(--line)',
  background: 'var(--bg-inset)', color: 'var(--tx-hi)', fontSize: 12,
};

export function btnStyle(variant: 'up' | 'down' | 'neutral' | 'brand', disabled: boolean): CSSProperties {
  const bg = variant === 'up' ? 'var(--up)' : variant === 'down' ? 'var(--down)' : variant === 'brand' ? 'var(--brand)' : 'var(--bg-panel-2)';
  return {
    padding: '8px 16px', borderRadius: 'var(--r)', border: variant === 'neutral' ? '1px solid var(--line)' : 'none',
    background: disabled ? 'var(--bg-void)' : bg,
    color: disabled ? 'var(--tx-faint)' : variant === 'neutral' ? 'var(--tx-hi)' : 'var(--bg)',
    fontSize: 12, fontWeight: 600, cursor: disabled ? 'not-allowed' : 'pointer', whiteSpace: 'nowrap',
  };
}

export function Empty({ text }: { text: string }) {
  return <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)', padding: 8 }}>{text}</div>;
}

export function Kpi({ label, value, color }: { label: string; value: string; color?: string }) {
  return (
    <div style={{ minWidth: 110 }}>
      <div className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)', textTransform: 'uppercase', letterSpacing: '.05em' }}>{label}</div>
      <div className="mono" style={{ fontSize: 15, fontWeight: 700, color: color ?? 'var(--tx-hi)' }}>{value}</div>
    </div>
  );
}
