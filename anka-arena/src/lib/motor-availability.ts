// motor-availability.ts — Detects "bot manager not initialized" from SWR errors.
//
// The backend answers 503 on /api/v1/bots and /api/v1/master/paper_mode when the
// multi-pair bot manager is not wired. apiFetch throws ApiError(status), so the
// HTTP status is the single source of truth (no inference from other flags).

import { ApiError } from '@/lib/api';

/** True when the error is an ApiError carrying HTTP 503. */
export function isServiceUnavailable(error: unknown): boolean {
  return error instanceof ApiError && error.status === 503;
}
