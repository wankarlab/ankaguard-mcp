'use client';

// permissions.ts — shared store mirroring GET /api/v1/me/permissions (C1.4).
//
// This is the SINGLE SOURCE OF TRUTH for screen locks and required levels.
// lib/rbac.ts is only a UX hint for instant render; once this store is loaded,
// LockedScreen and NavRail must defer to it. Nothing here is hardcoded — every
// value (required levels, lock state, kyc status) comes from the backend, which
// reads the dynamic config table (rbac.level.*).

import { create } from 'zustand';
import { fetchPermissions } from '@/lib/api';
import type { Permissions, ScreenLock } from '@/lib/adapters';

interface PermissionsState {
  permissions: Permissions | null;
  loading:     boolean;
  loaded:      boolean;
  error:       string | null;

  /** Fetch permissions once (no-op if already loaded unless force=true). */
  load: (force?: boolean) => Promise<void>;
  /** Clear on logout. */
  reset: () => void;
  /** Lock descriptor for a screen slug, or null if not present / not locked. */
  lockFor: (screen: string) => ScreenLock | null;
  /** True when the screen is locked (defaults to false when unknown). */
  isLocked: (screen: string) => boolean;
}

export const usePermissionsStore = create<PermissionsState>((set, get) => ({
  permissions: null,
  loading:     false,
  loaded:      false,
  error:       null,

  load: async (force = false) => {
    const { loaded, loading } = get();
    if (loading) return;
    if (loaded && !force) return;
    set({ loading: true, error: null });
    try {
      const permissions = await fetchPermissions();
      set({ permissions, loading: false, loaded: true });
    } catch (e) {
      set({
        loading: false,
        loaded:  true,
        error:   e instanceof Error ? e.message : 'No se pudieron cargar los permisos.',
      });
    }
  },

  reset: () => set({ permissions: null, loading: false, loaded: false, error: null }),

  lockFor: (screen) => {
    const locks = get().permissions?.locks ?? [];
    return locks.find((l) => l.screen === screen) ?? null;
  },

  isLocked: (screen) => {
    const lock = get().lockFor(screen);
    return lock?.locked ?? false;
  },
}));
