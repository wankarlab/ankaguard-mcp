// killswitch-status.ts — Typed wrapper + helpers for GET /api/v1/admin/killswitch/status.
//
// Mirrors handlers.KillSwitchStatus (snake_case). The backend keeps this state in
// memory, so after a restart it can legitimately be all-empty: show exactly what
// is reported, never infer previous states.

import { apiFetch, ApiError } from '@/lib/api';

export interface KillSwitchStatus {
  ring0_active: boolean;
  ring1_specs: string[] | null;
  ring2_tenants: string[] | null;
  last_action?: string;
  acted_by?: string;
  acted_at?: string;
}

export function getKillSwitchStatus(): Promise<KillSwitchStatus> {
  return apiFetch<KillSwitchStatus>('/api/v1/admin/killswitch/status');
}

/** True when the error means the caller may not read the status (401/403). */
export function isStatusForbidden(error: unknown): boolean {
  return error instanceof ApiError && (error.status === 401 || error.status === 403);
}
