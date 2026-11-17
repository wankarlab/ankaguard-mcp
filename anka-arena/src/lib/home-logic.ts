// home-logic.ts — pure helpers for the /home blocks (no network, no React).

import type { HealthComponent } from '@/lib/home-api';

export interface LogbookEntry {
  id: string;
  /** Unix ms. */
  ts: number;
  kind: string;
  text: string;
}

interface HistoryRow {
  id: string;
  kind: string;
  message: string;
  ts: string;
}

interface LiveRow {
  id: string;
  kind: string;
  txt: string;
  ts: number;
}

/**
 * Merge persisted flywheel events with live stream events into one list,
 * newest first, de-duplicated by id and capped at `max`. Rows with an
 * unparseable timestamp are dropped rather than given an invented time.
 */
export function mergeLogbook(
  history: HistoryRow[] | null | undefined,
  live: LiveRow[] | null | undefined,
  max = 20,
): LogbookEntry[] {
  const byId = new Map<string, LogbookEntry>();
  for (const h of history ?? []) {
    const ts = Date.parse(h.ts);
    if (Number.isNaN(ts)) continue;
    byId.set(h.id, { id: h.id, ts, kind: h.kind, text: h.message });
  }
  for (const l of live ?? []) {
    if (!Number.isFinite(l.ts)) continue;
    byId.set(l.id, { id: l.id, ts: l.ts, kind: l.kind, text: l.txt });
  }
  return [...byId.values()].sort((a, b) => b.ts - a.ts).slice(0, max);
}

/** Components that are not ok, critical ones first. */
export function affectedComponents(components: HealthComponent[] | null | undefined): HealthComponent[] {
  return (components ?? [])
    .filter((c) => c.status !== 'ok')
    .sort((a, b) => Number(b.critical) - Number(a.critical));
}

/** Format a number, or "—" when the value is missing / not finite. */
export function fmtOrDash(v: number | null | undefined, fmt: (n: number) => string): string {
  return v == null || !Number.isFinite(v) ? '—' : fmt(v);
}
