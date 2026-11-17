'use client';

// consent.ts — shared store for consent gates (C1.3).
//
// Consent state lives in the BACKEND (consent_record table), NOT localStorage.
// This store is just an in-memory cache of GET /consent/status keyed by `kind`.
// The legal text comes from GET /consent/template/:kind (versioned, auditable).
// A user must re-consent when a newer template version is published — compare
// status.acceptedVersion to the rendered template.version.

import { create } from 'zustand';
import { fetchConsentStatus, acceptConsent } from '@/lib/api';
import type { ConsentStatus } from '@/lib/adapters';

interface ConsentState {
  /** Latest known status per consent kind (e.g. "copy_trading"). */
  statusByKind: Record<string, ConsentStatus>;
  loadingKind:  Record<string, boolean>;

  /** Refresh the acceptance status for a kind from the backend. */
  refresh: (kind: string) => Promise<ConsentStatus | null>;
  /** Accept a consent version (idempotent on the backend) and cache the result. */
  accept: (kind: string, templateVersion: number) => Promise<ConsentStatus>;
  /**
   * True when the user has accepted at least `requiredVersion` of `kind`.
   * Returns false when unknown (forces the modal to open and verify).
   */
  hasAccepted: (kind: string, requiredVersion: number) => boolean;
  /** Clear cache on logout. */
  reset: () => void;
}

export const useConsentStore = create<ConsentState>((set, get) => ({
  statusByKind: {},
  loadingKind:  {},

  refresh: async (kind) => {
    set((s) => ({ loadingKind: { ...s.loadingKind, [kind]: true } }));
    try {
      const status = await fetchConsentStatus(kind);
      set((s) => ({
        statusByKind: { ...s.statusByKind, [kind]: status },
        loadingKind:  { ...s.loadingKind, [kind]: false },
      }));
      return status;
    } catch {
      set((s) => ({ loadingKind: { ...s.loadingKind, [kind]: false } }));
      return null;
    }
  },

  accept: async (kind, templateVersion) => {
    const res = await acceptConsent(kind, templateVersion);
    const status: ConsentStatus = {
      kind,
      accepted:        res.accepted,
      acceptedVersion: res.accepted_version,
    };
    set((s) => ({ statusByKind: { ...s.statusByKind, [kind]: status } }));
    return status;
  },

  hasAccepted: (kind, requiredVersion) => {
    const status = get().statusByKind[kind];
    if (!status || !status.accepted) return false;
    return status.acceptedVersion >= requiredVersion;
  },

  reset: () => set({ statusByKind: {}, loadingKind: {} }),
}));
