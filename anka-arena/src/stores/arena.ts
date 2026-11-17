'use client';

import { create } from 'zustand';
import type { WSTradeUpdate } from '@/hooks/useArenaStream';

// ── Domain types ──────────────────────────────────────────────

export interface TournamentParticipant {
  userId:       string;
  username:     string;
  rank:         number;
  pnlUsd:       number;
  pnlPct:       number;
  trades:       number;
  winRate:      number;
  tier:         'paper' | 'micro' | 'real';
  isMe:         boolean;
  roundsWon?:   number;
}

export interface TournamentRound {
  id:        string;
  name:      string;
  startAt:   number;  // unix ms
  endAt:     number;
  status:    'upcoming' | 'active' | 'ended';
  prizePool: number;
  currency:  string;
}

export type ArenaPhase = 'LOBBY' | 'ACTIVE' | 'VOTING' | 'SETTLE' | 'RECAP';

export interface TradeHistoryItem {
  id:      string;
  pair:    string;
  side:    'LONG' | 'SHORT';
  pnl_usd: number;
  ts:      number;
}

export interface Competitor {
  id:       string;
  name:     string;
  provider: string;
  pnl_usd:  number;
  pnl_pct:  number;
  win_rate: number;
  current_trade?: {
    pair:  string;
    side:  'LONG' | 'SHORT';
    entry: number;
  };
  trades: TradeHistoryItem[];
}

// ── Store interface ───────────────────────────────────────────

export interface ArenaState {
  // Tournament meta
  currentRound:  TournamentRound | null;

  // Leaderboard
  leaderboard:   TournamentParticipant[];
  myRank:        number | null;
  myPnlUsd:      number;
  myPnlPct:      number;

  // AI competitors (for CompetitorColumn)
  competitors:   Competitor[];

  // Round phase & timer
  phase:            ArenaPhase;
  secondsRemaining: number;

  // Voting
  votes:         Record<string, number>;   // competitorId -> vote count
  myVote:        string | null;            // competitorId I voted for this round

  // Narration
  narrations:    string[];                 // newest first, max 10

  // Live PnL queue (rAF-coalesced — legacy leaderboard path)
  pendingPnlUpdates: Map<string, number>;
  rafId:         number | null;

  // Status
  loading:       boolean;
  error:         string | null;

  // Actions
  setRound:             (round: TournamentRound | null) => void;
  setLeaderboard:       (participants: TournamentParticipant[]) => void;
  setCompetitors:       (competitors: Competitor[]) => void;
  setPhase:             (phase: ArenaPhase) => void;
  setSecondsRemaining:  (s: number) => void;
  setVotes:             (votes: Record<string, number>) => void;
  mergeVotes:           (patch: Record<string, number>) => void;
  castVote:             (competitorId: string) => void;
  pushNarration:        (text: string) => void;

  // PnL actions
  applyPnlUpdate:       (userId: string, pnlUsd: number) => void;
  flushPnlUpdates:      () => void;
  applyCompetitorPnl:   (competitorId: string, pnlUsd: number, pnlPct: number) => void;
  applyTradeUpdate:     (msg: WSTradeUpdate) => void;
  applyVoteUpdate:      (competitorId: string, votes: number) => void;

  setLoading: (v: boolean) => void;
  setError:   (msg: string | null) => void;
  reset:      () => void;
}

// ── Store ─────────────────────────────────────────────────────

const MAX_NARRATIONS = 10;

export const useArenaStore = create<ArenaState>()((set, get) => ({
  currentRound:       null,
  leaderboard:        [],
  myRank:             null,
  myPnlUsd:           0,
  myPnlPct:           0,
  competitors:        [],
  phase:              'LOBBY',
  secondsRemaining:   0,
  votes:              {},
  myVote:             null,
  narrations:         [],
  pendingPnlUpdates:  new Map(),
  rafId:              null,
  loading:            true,
  error:              null,

  setRound: (round) => set({ currentRound: round }),

  setLeaderboard: (participants) => {
    const me = participants.find((p) => p.isMe);
    set({
      leaderboard: participants,
      myRank:      me?.rank ?? null,
      myPnlUsd:    me?.pnlUsd ?? 0,
      myPnlPct:    me?.pnlPct ?? 0,
      loading:     false,
    });
  },

  setCompetitors: (competitors) => set({ competitors }),

  setPhase: (phase) => set({ phase }),

  setSecondsRemaining: (s) => set({ secondsRemaining: s }),

  setVotes: (votes) => set({ votes }),

  mergeVotes: (patch) =>
    set((s) => ({ votes: { ...s.votes, ...patch } })),

  castVote: (competitorId) => set({ myVote: competitorId }),

  pushNarration: (text) =>
    set((s) => ({
      narrations: [text, ...s.narrations].slice(0, MAX_NARRATIONS),
    })),

  // ── PnL (leaderboard path) ────────────────────────────────

  applyPnlUpdate: (userId, pnlUsd) => {
    const { pendingPnlUpdates, rafId, flushPnlUpdates } = get();
    pendingPnlUpdates.set(userId, pnlUsd);
    if (rafId === null && typeof requestAnimationFrame !== 'undefined') {
      const id = requestAnimationFrame(() => {
        set({ rafId: null });
        flushPnlUpdates();
      });
      set({ rafId: id });
    }
  },

  flushPnlUpdates: () => {
    const { pendingPnlUpdates, leaderboard } = get();
    if (pendingPnlUpdates.size === 0) return;

    const updated = leaderboard.map((p) => {
      const newPnl = pendingPnlUpdates.get(p.userId);
      return newPnl !== undefined ? { ...p, pnlUsd: newPnl } : p;
    });

    updated.sort((a, b) => b.pnlUsd - a.pnlUsd);
    updated.forEach((p, i) => { p.rank = i + 1; });

    const me = updated.find((p) => p.isMe);
    pendingPnlUpdates.clear();

    set({
      leaderboard:       updated,
      myRank:            me?.rank ?? null,
      myPnlUsd:          me?.pnlUsd ?? 0,
      pendingPnlUpdates: new Map(),
    });
  },

  // ── PnL (competitor column path) ─────────────────────────

  applyCompetitorPnl: (competitorId, pnlUsd, pnlPct) =>
    set((s) => ({
      competitors: s.competitors.map((c) =>
        c.id === competitorId ? { ...c, pnl_usd: pnlUsd, pnl_pct: pnlPct } : c,
      ),
    })),

  applyTradeUpdate: (msg) =>
    set((s) => ({
      competitors: s.competitors.map((c) => {
        if (c.id !== msg.competitor) return c;
        const newTrade = {
          id:      `${msg.competitor}-${Date.now()}`,
          pair:    msg.pair,
          side:    msg.side,
          pnl_usd: msg.pnl_usd,
          ts:      Date.now(),
        };
        return {
          ...c,
          pnl_usd: msg.pnl_usd,
          current_trade: { pair: msg.pair, side: msg.side, entry: msg.entry },
          // Prepend to history, keep last 20
          trades: [newTrade, ...c.trades].slice(0, 20),
        };
      }),
    })),

  applyVoteUpdate: (competitorId, votes) =>
    set((s) => ({ votes: { ...s.votes, [competitorId]: votes } })),

  setLoading: (v) => set({ loading: v }),
  setError:   (msg) => set({ error: msg, loading: false }),

  reset: () =>
    set({
      currentRound:      null,
      leaderboard:       [],
      myRank:            null,
      myPnlUsd:          0,
      myPnlPct:          0,
      competitors:       [],
      phase:             'LOBBY',
      secondsRemaining:  0,
      votes:             {},
      myVote:            null,
      narrations:        [],
      pendingPnlUpdates: new Map(),
      rafId:             null,
      loading:           true,
      error:             null,
    }),
}));
