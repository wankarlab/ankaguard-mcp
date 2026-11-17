'use client';

import { create } from 'zustand';

export interface EdgeStats {
  pair:        string;
  profitFactor: number;
  winRate:     number;
  certified:   boolean;
}

export interface EngineStats {
  // Capital ladder
  capitalUsd:     number;
  tier:           'paper' | 'micro' | 'real';
  dailyPnlUsd:    number;
  dailyPnlPct:    number;
  weeklyPnlUsd:   number;

  // Flywheel metrics
  aumCertUsd:     number;
  activeBots:     number;
  maxBots:        number;
  edgesTotal:     number;
  edgesCertified: number;
  featureStoreRows:    number; // real feature_store row count from backend
  specsInQueue:        number;
  specsCertifiedToday: number;
  gatePassPct:    number;   // % of entries passing regime+ML gate
  gbmPf:          number;   // GBM out-of-sample profit factor
  gbmAuc:         number;   // GBM AUC score
  gbmWinRate:     number;

  // Live status
  exchangeStatus: 'live' | 'degraded' | 'offline';
  mlServerUp:     boolean;
  regime:         string;   // current detected regime (TRENDING|RANGING|BREAKOUT|UNKNOWN)

  // Per-edge breakdown
  edges: EdgeStats[];

  // Last update
  updatedAt: number; // unix ms
}

const DEFAULT_STATS: EngineStats = {
  capitalUsd:     0,
  tier:           'paper',
  dailyPnlUsd:    0,
  dailyPnlPct:    0,
  weeklyPnlUsd:   0,
  aumCertUsd:     0,
  activeBots:     0,
  maxBots:        5,
  edgesTotal:     0,
  edgesCertified: 0,
  featureStoreRows:    0,
  specsInQueue:        0,
  specsCertifiedToday: 0,
  gatePassPct:    0,
  gbmPf:          0,
  gbmAuc:         0,
  gbmWinRate:     0,
  exchangeStatus: 'offline',
  mlServerUp:     false,
  regime:         'UNKNOWN',
  edges:          [],
  updatedAt:      0,
};

interface EngineState {
  stats: EngineStats;
  loading: boolean;
  error: string | null;
  setStats: (stats: Partial<EngineStats>) => void;
  setLoading: (loading: boolean) => void;
  setError: (error: string | null) => void;
  reset: () => void;
}

export const useEngineStore = create<EngineState>()((set) => ({
  stats:   DEFAULT_STATS,
  loading: true,
  error:   null,

  setStats: (incoming) =>
    set((s) => ({
      stats:   { ...s.stats, ...incoming, updatedAt: Date.now() },
      loading: false,
      error:   null,
    })),

  setLoading: (loading) => set({ loading }),

  setError: (error) => set({ error, loading: false }),

  reset: () => set({ stats: DEFAULT_STATS, loading: true, error: null }),
}));
