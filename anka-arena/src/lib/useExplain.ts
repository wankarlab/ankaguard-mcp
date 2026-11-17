'use client';

// useExplain — open the ExplainDrawer from any surface given a tradeId (C1.2).
//
// The drawer state (drawerOpen / drawerTradeId / drawerVariant) lives in the UI
// store; the singleton <ExplainDrawer/> in the layout reacts to it and fetches
// GET /api/v1/trades/:id/explain. This hook is the canonical entry point so any
// trade row, chart marker, or feed item can deep-link into explainability
// without importing the store shape directly.

import { useCallback } from 'react';
import { useUIStore } from '@/stores/ui';
import type { DrawerVariant } from '@/stores/ui';

export interface UseExplain {
  /** Open the ExplainDrawer for a trade. Optionally pick the initial tab. */
  openExplain: (tradeId: string, variant?: DrawerVariant) => void;
  /** Close the drawer. */
  closeExplain: () => void;
  /** Whether the drawer is currently open. */
  isOpen: boolean;
  /** The trade id the drawer is currently showing (null when closed). */
  activeTradeId: string | null;
}

export function useExplain(): UseExplain {
  const openExplainAction = useUIStore((s) => s.openExplain);
  const closeDrawer       = useUIStore((s) => s.closeDrawer);
  const isOpen            = useUIStore((s) => s.drawerOpen);
  const activeTradeId     = useUIStore((s) => s.drawerTradeId);

  const openExplain = useCallback(
    (tradeId: string, variant: DrawerVariant = 'tree') => {
      if (!tradeId) return;
      openExplainAction(tradeId, variant);
    },
    [openExplainAction],
  );

  return { openExplain, closeExplain: closeDrawer, isOpen, activeTradeId };
}

export default useExplain;
