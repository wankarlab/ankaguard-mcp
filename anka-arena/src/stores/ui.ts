'use client';

import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import type { RoleId } from '@/lib/rbac';
import { clearToken, getCurrentUser } from '@/lib/auth';
import { usePermissionsStore } from '@/stores/permissions';
import { useConsentStore } from '@/stores/consent';

export type DrawerVariant = 'tree' | 'chain' | 'replay';

interface UIState {
  // Role / auth state
  isLoggedIn: boolean;
  role: RoleId;
  kycLevel: number;

  // Explain drawer (trade detail / decision tree)
  drawerOpen: boolean;
  drawerTradeId: string | null;
  drawerVariant: DrawerVariant;

  // Kill switch modal
  killSwitchOpen: boolean;

  // Actions
  openExplain: (tradeId: string, variant?: DrawerVariant) => void;
  closeDrawer: () => void;
  openKillSwitch: () => void;
  closeKillSwitch: () => void;
  setRole: (role: RoleId, kyc: number) => void;
  login: (role: RoleId, kyc: number) => void;
  logout: () => void;
  /** Re-derive auth state from the stored JWT (call on app mount). */
  hydrate: () => void;
}

export const useUIStore = create<UIState>()(
  persist(
    (set) => ({
      // Defaults: viewer with KYC 0 until token is decoded
      isLoggedIn: false,
      role:     'viewer',
      kycLevel: 0,

      drawerOpen:    false,
      drawerTradeId: null,
      drawerVariant: 'tree',

      killSwitchOpen: false,

      openExplain: (tradeId, variant = 'tree') =>
        set({ drawerOpen: true, drawerTradeId: tradeId, drawerVariant: variant }),

      closeDrawer: () =>
        set({ drawerOpen: false, drawerTradeId: null }),

      openKillSwitch: () =>
        set({ killSwitchOpen: true }),

      closeKillSwitch: () =>
        set({ killSwitchOpen: false }),

      setRole: (role, kyc) =>
        set({ role, kycLevel: kyc }),

      login: (role, kyc) =>
        set({ isLoggedIn: true, role, kycLevel: kyc }),

      logout: () => {
        clearToken();
        // Clear backend-derived caches so the next user starts clean.
        usePermissionsStore.getState().reset();
        useConsentStore.getState().reset();
        set({ isLoggedIn: false, role: 'viewer', kycLevel: 0 });
      },

      hydrate: () => {
        const claims = getCurrentUser();
        if (claims) {
          set({ isLoggedIn: true, role: claims.role, kycLevel: claims.kyc });
        } else {
          set({ isLoggedIn: false, role: 'viewer', kycLevel: 0 });
        }
      },
    }),
    {
      name: 'anka-arena-ui',
      // Only persist auth role and KYC level — not transient UI state
      partialize: (state) => ({
        isLoggedIn: state.isLoggedIn,
        role:     state.role,
        kycLevel: state.kycLevel,
      }),
    },
  ),
);
