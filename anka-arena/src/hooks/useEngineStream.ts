'use client';

import { useEffect, useRef, useState } from 'react';
import { getWSClient } from '@/lib/ws';
import { useEngineStore } from '@/stores/engine';

// ──────────────────────────────────────────────────────────────
//  Types
// ──────────────────────────────────────────────────────────────

export type FlywheelEventKind =
  | 'gate_decision'
  | 'trade_close'
  | 'spec_promoted'
  | 'retrain'
  | 'cohort';

export interface FlywheelEvent {
  id:        string;
  kind:      FlywheelEventKind;
  ts:        number;   // unix ms
  txt:       string;
  tone:      'up' | 'down' | 'bad' | 'paper' | 'brand' | 'micro' | 'neutral';
  tradeId?:  string;   // present on trade_close — enables ExplainDrawer
  real?:     boolean;  // true = real-money trade
}

// Raw WS payloads from the server ─────────────────────────────
interface WSTradeClosed {
  type:      'trade.closed';
  trade_id:  string;
  pair:      string;
  direction: 'LONG' | 'SHORT';
  pnl_usd:   number;
  pnl_pct:   number;
  real?:     boolean;
}

interface WSGateDecided {
  type:       'gate.decided';
  pair:       string;
  spec_id:    string;
  passed:     boolean;
  pf_oos?:    number;
  reason?:    string;
}

interface WSSpecPromoted {
  type:       'spec.promoted';
  spec_id:    string;
  from_tier:  string;
  to_tier:    string;
}

type WSEngineStats = {
  type: 'engine.stats';
  [k: string]: unknown;
};

type WSPayload = WSTradeClosed | WSGateDecided | WSSpecPromoted | WSEngineStats;

// ──────────────────────────────────────────────────────────────
//  Helpers
// ──────────────────────────────────────────────────────────────

const MAX_EVENTS = 50;

function uid() {
  return Math.random().toString(36).slice(2, 9);
}

function parseTrade(msg: WSTradeClosed): FlywheelEvent {
  const sign  = msg.pnl_usd >= 0 ? '+' : '';
  const pct   = msg.pnl_pct.toFixed(2);
  return {
    id:      uid(),
    kind:    'trade_close',
    ts:      Date.now(),
    txt:     `${msg.pair} ${msg.direction} cerrado ${sign}$${msg.pnl_usd.toFixed(2)} · MFE ${sign}${pct}%`,
    tone:    msg.pnl_usd >= 0 ? 'up' : 'bad',
    tradeId: msg.trade_id,
    real:    msg.real ?? false,
  };
}

function parseGate(msg: WSGateDecided): FlywheelEvent {
  const passed = msg.passed;
  const pf     = msg.pf_oos !== undefined ? ` · PF ${msg.pf_oos.toFixed(2)}` : '';
  const reason = msg.reason ? ` · ${msg.reason}` : '';
  return {
    id:   uid(),
    kind: 'gate_decision',
    ts:   Date.now(),
    txt:  passed
      ? `GATE certificó "${msg.spec_id}"${pf}`
      : `GATE rechazó "${msg.spec_id}"${pf}${reason}`,
    tone: passed ? 'up' : 'bad',
  };
}

function parsePromo(msg: WSSpecPromoted): FlywheelEvent {
  return {
    id:   uid(),
    kind: 'spec_promoted',
    ts:   Date.now(),
    txt:  `"${msg.spec_id}" promovida ${msg.from_tier} → ${msg.to_tier}`,
    tone: 'paper',
  };
}

// ──────────────────────────────────────────────────────────────
//  Hook
// ──────────────────────────────────────────────────────────────

interface UseEngineStreamResult {
  events:    FlywheelEvent[];
  connected: boolean;
}

export function useEngineStream(): UseEngineStreamResult {
  const setStats   = useEngineStore((s) => s.setStats);
  const [events, setEvents]       = useState<FlywheelEvent[]>([]);
  const [connected, setConnected] = useState(false);

  // Stable ref so subscription callbacks don't stale-close
  const eventsRef = useRef<FlywheelEvent[]>(events);
  eventsRef.current = events;

  useEffect(() => {
    // SSR guard
    if (typeof window === 'undefined') return;

    let ws: ReturnType<typeof getWSClient>;
    try {
      ws = getWSClient();
    } catch {
      return;
    }

    // Track connection status
    const pollId = setInterval(() => {
      setConnected(ws.isConnected);
    }, 1500);

    function pushEvent(e: FlywheelEvent) {
      setEvents((prev) => {
        const next = [e, ...prev];
        // Backpressure: keep only MAX_EVENTS most recent
        return next.length > MAX_EVENTS ? next.slice(0, MAX_EVENTS) : next;
      });
    }

    const unsubs: Array<() => void> = [];

    // trade.closed
    unsubs.push(
      ws.subscribe('trade.closed', (raw) => {
        const msg = raw as WSPayload as WSTradeClosed;
        pushEvent(parseTrade(msg));
        // Refresh stats after close (let SWR handle it via revalidation)
      }),
    );

    // gate.decided
    unsubs.push(
      ws.subscribe('gate.decided', (raw) => {
        pushEvent(parseGate(raw as WSGateDecided));
      }),
    );

    // spec.promoted
    unsubs.push(
      ws.subscribe('spec.promoted', (raw) => {
        pushEvent(parsePromo(raw as WSSpecPromoted));
      }),
    );

    // engine.stats — live push (supplements SWR polling)
    unsubs.push(
      ws.subscribe('engine.stats', (raw) => {
        const msg = raw as WSEngineStats;
        // Strip the 'type' key before merging into store
        // eslint-disable-next-line @typescript-eslint/no-unused-vars
        const { type: _t, ...patch } = msg;
        setStats(patch as Parameters<typeof setStats>[0]);
      }),
    );

    return () => {
      clearInterval(pollId);
      unsubs.forEach((u) => u());
    };
  }, [setStats]);

  return { events, connected };
}
