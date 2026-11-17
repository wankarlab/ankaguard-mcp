'use client';

/**
 * useArenaStream — Central arena WebSocket hook.
 *
 * - Subscribes to the global WSClient singleton.
 * - Applies rAF-coalescing for pnl.update and vote.update.
 * - Backpressure: if buffer exceeds 100 events, discards intermediate pnl.update
 *   (keeps only the last value per competitor).
 * - Late-join: on mount, fetches GET /api/v1/tournament/:id for initial snapshot.
 */

import { useEffect, useRef } from 'react';
import { getWSClient } from '@/lib/ws';
import { useArenaStore } from '@/stores/arena';
import { api } from '@/lib/api';

// ── WS message shapes ────────────────────────────────────────

export interface WSPnlUpdate {
  type:        'pnl.update';
  competitor:  string;   // provider id / name
  pnl_usd:    number;
  pnl_pct:    number;
}

export interface WSVoteUpdate {
  type:       'vote.update';
  competitor: string;
  votes:      number;
  total:      number;
}

export interface WSRoundPhase {
  type:              'round.phase';
  phase:             'LOBBY' | 'ACTIVE' | 'VOTING' | 'SETTLE' | 'RECAP';
  seconds_remaining: number;
  round_id:          string;
}

export interface WSTradeUpdate {
  type:       'trade.update';
  competitor: string;
  pair:       string;
  side:       'LONG' | 'SHORT';
  entry:      number;
  pnl_usd:   number;
}

export interface WSNarratorSpeech {
  type: 'narrator.speech';
  text: string;
  ts:   number;
}

export interface WSLeaderboard {
  type:         'leaderboard.update';
  participants: import('@/stores/arena').TournamentParticipant[];
}

// ── Backpressure buffer ───────────────────────────────────────

const BACKPRESSURE_THRESHOLD = 100;

// ── Snapshot response from GET /api/v1/tournament/:id ────────

interface TournamentSnapshot {
  id:          string;
  name:        string;
  status:      'LOBBY' | 'ACTIVE' | 'VOTING' | 'SETTLE' | 'RECAP' | 'ENDED';
  phase:       'LOBBY' | 'ACTIVE' | 'VOTING' | 'SETTLE' | 'RECAP';
  seconds_remaining: number;
  round_id:    string;
  start_at:    number;
  end_at:      number;
  prize_pool:  number;
  currency:    string;
  participants: import('@/stores/arena').TournamentParticipant[];
  competitors: {
    id:       string;
    name:     string;
    provider: string;
    pnl_usd: number;
    pnl_pct: number;
    win_rate: number;
    current_trade?: {
      pair:  string;
      side:  'LONG' | 'SHORT';
      entry: number;
    };
    trades: {
      id:     string;
      pair:   string;
      side:   'LONG' | 'SHORT';
      pnl_usd: number;
      ts:     number;
    }[];
  }[];
  votes: Record<string, number>;
}

export function useArenaStream(tournamentId: string): void {
  const {
    setRound,
    setLeaderboard,
    applyPnlUpdate,
    setPhase,
    setSecondsRemaining,
    applyVoteUpdate,
    pushNarration,
    setCompetitors,
    setVotes,
    setLoading,
    setError,
  } = useArenaStore();

  // rAF state for pnl + vote coalescing
  const rafPnlId   = useRef<number | null>(null);
  const rafVoteId  = useRef<number | null>(null);
  const pnlBuffer  = useRef<Map<string, { pnl_usd: number; pnl_pct: number }>>(new Map());
  const voteBuffer = useRef<Map<string, number>>(new Map());
  const pnlCount   = useRef(0);

  useEffect(() => {
    const pnlBuf = pnlBuffer.current;
    const voteBuf = voteBuffer.current;
    
    if (typeof window === 'undefined') return;
    if (!tournamentId) return;

    // ── Late-join snapshot ────────────────────────────────────
    setLoading(true);
    api.get<TournamentSnapshot>(`/api/v1/tournament/${tournamentId}`)
      .then((snap) => {
        setRound({
          id:        snap.id,
          name:      snap.name,
          startAt:   snap.start_at,
          endAt:     snap.end_at,
          status:    snap.status === 'ENDED' ? 'ended'
                   : snap.status === 'LOBBY' ? 'upcoming'
                   : 'active',
          prizePool: snap.prize_pool,
          currency:  snap.currency,
        });
        setLeaderboard(snap.participants);
        setCompetitors(snap.competitors);
        setPhase(snap.phase);
        setSecondsRemaining(snap.seconds_remaining);
        if (snap.votes) setVotes(snap.votes);
        setLoading(false);
      })
      .catch((err) => {
        setError(err?.message ?? 'Error al cargar torneo');
      });

    // ── WS subscription ───────────────────────────────────────
    let ws: ReturnType<typeof getWSClient>;
    try {
      ws = getWSClient();
    } catch {
      return;
    }

    const unsubs: Array<() => void> = [];

    // pnl.update — rAF-coalesced with backpressure
    unsubs.push(
      ws.subscribe('pnl.update', (raw) => {
        const msg = raw as WSPnlUpdate;
        pnlCount.current += 1;

        // Backpressure: if buffer exceeds threshold, discard intermediate updates
        // (the Map overwrites older values for the same competitor — only last matters)
        if (pnlCount.current > BACKPRESSURE_THRESHOLD) {
          // Only keep the latest per competitor — the Map.set() below handles this
          // but we skip scheduling a new rAF if one is already pending
        }

        pnlBuffer.current.set(msg.competitor, {
          pnl_usd: msg.pnl_usd,
          pnl_pct: msg.pnl_pct,
        });

        if (rafPnlId.current === null) {
          rafPnlId.current = requestAnimationFrame(() => {
            rafPnlId.current = null;
            pnlCount.current = 0;
            pnlBuffer.current.forEach((data, competitorId) => {
              applyPnlUpdate(competitorId, data.pnl_usd);
              useArenaStore.getState().applyCompetitorPnl(competitorId, data.pnl_usd, data.pnl_pct);
            });
            pnlBuffer.current.clear();
          });
        }
      }),
    );

    // vote.update — rAF-coalesced
    unsubs.push(
      ws.subscribe('vote.update', (raw) => {
        const msg = raw as WSVoteUpdate;
        voteBuffer.current.set(msg.competitor, msg.votes);

        if (rafVoteId.current === null) {
          rafVoteId.current = requestAnimationFrame(() => {
            rafVoteId.current = null;
            const patch: Record<string, number> = {};
            voteBuffer.current.forEach((votes, id) => {
              patch[id] = votes;
            });
            voteBuffer.current.clear();
            useArenaStore.getState().mergeVotes(patch);
          });
        }
      }),
    );

    // round.phase — phase / timer transitions
    unsubs.push(
      ws.subscribe('round.phase', (raw) => {
        const msg = raw as WSRoundPhase;
        setPhase(msg.phase);
        setSecondsRemaining(msg.seconds_remaining);
      }),
    );

    // trade.update — competitor current trade
    unsubs.push(
      ws.subscribe('trade.update', (raw) => {
        const msg = raw as WSTradeUpdate;
        useArenaStore.getState().applyTradeUpdate(msg);
      }),
    );

    // narrator.speech — LLM narration feed
    unsubs.push(
      ws.subscribe('narrator.speech', (raw) => {
        const msg = raw as WSNarratorSpeech;
        pushNarration(msg.text);
      }),
    );

    // leaderboard.update
    unsubs.push(
      ws.subscribe('leaderboard.update', (raw) => {
        const msg = raw as WSLeaderboard;
        setLeaderboard(msg.participants);
      }),
    );

    return () => {
      unsubs.forEach((u) => u());
      if (rafPnlId.current !== null) {
        cancelAnimationFrame(rafPnlId.current);
        rafPnlId.current = null;
      }
      if (rafVoteId.current !== null) {
        cancelAnimationFrame(rafVoteId.current);
        rafVoteId.current = null;
      }
      pnlBuf.clear();
      voteBuf.clear();
    };
  }, [
    tournamentId,
    setRound, setLeaderboard, applyPnlUpdate, setPhase,
    setSecondsRemaining, applyVoteUpdate, pushNarration,
    setCompetitors, setVotes, setLoading, setError,
  ]);
}

/**
 * Isolated PnL slice hook — subscribes only to competitor PnL in the store.
 * Prevents re-renders of parent tree when PnL changes.
 */
export function usePnLSlice(competitorId: string): { pnl_usd: number; pnl_pct: number } {
  return useArenaStore((s) => {
    const c = s.competitors.find((c) => c.id === competitorId);
    return { pnl_usd: c?.pnl_usd ?? 0, pnl_pct: c?.pnl_pct ?? 0 };
  });
}
