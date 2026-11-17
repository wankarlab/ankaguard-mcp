// home-api.ts — typed wrappers for the endpoints consumed only by /home.
// Shapes mirror internal/api/v1/handlers/system_health.go and engine_handler.go.

import { apiFetch } from '@/lib/api';

export type HealthState = 'ok' | 'degraded' | 'down';

export interface HealthComponent {
  name: string;
  critical: boolean;
  status: 'ok' | 'down';
  latency_ms: number;
  message: string;
}

export interface SystemHealth {
  status: HealthState;
  engine_real_money: boolean | null;
  components: HealthComponent[] | null;
  checked_at: string;
}

export function getSystemHealth(): Promise<SystemHealth> {
  return apiFetch<SystemHealth>('/api/v1/system/health');
}

export interface FlywheelEventRow {
  id: string;
  kind: string;
  message: string;
  cap_state: string;
  /** ISO timestamp. */
  ts: string;
}

export interface FlywheelEventsResponse {
  events: FlywheelEventRow[] | null;
  total: number;
}

export function getFlywheelEvents(limit = 20): Promise<FlywheelEventsResponse> {
  return apiFetch<FlywheelEventsResponse>(`/api/v1/flywheel/events?limit=${limit}`);
}
