// account-api.ts — real account equity history (GET /api/v1/account/equity-history).
import { apiFetch } from '@/lib/api';

export type EquityRange = '24h' | '7d' | '30d' | 'all';
export const EQUITY_RANGES: EquityRange[] = ['24h', '7d', '30d', 'all'];

export interface AccountEquityPoint {
  ts: string;
  total_equity_usd: number;
}

export interface AccountEquityMetrics {
  current: number;
  peak: number;
  peak_at: string;
  max_drawdown_pct: number;
  current_drawdown_pct: number;
  change_abs: number;
  /** null when the first value is not positive (undefined). */
  change_pct: number | null;
}

export interface AccountEquityHistory {
  range: string;
  points: AccountEquityPoint[] | null;
  total_points: number;
  reduced: boolean;
  /** null when the range has no snapshots. */
  metrics: AccountEquityMetrics | null;
}

export async function getAccountEquityHistory(range: EquityRange): Promise<AccountEquityHistory> {
  return apiFetch<AccountEquityHistory>(`/api/v1/account/equity-history?range=${range}`);
}
