// motor-api.ts — Typed wrappers for the "Motor" operational-control surface.
//
// These endpoints are served by the trading-mcp backend (master agent + bot
// manager) and reached through the gateway (:9090, JWT) / Next.js proxy. Every
// call uses apiFetch, which injects the Bearer token and auto-refreshes on 401.
//
// IMPORTANT shape note: the master agent's MasterState and MarketOpportunity Go
// structs carry NO json tags, so they marshal with PascalCase keys. The bot
// manager's BotInfo and the xsec handler DO use snake_case json tags. The types
// below mirror the real wire format exactly — do not "normalize" them.

import { apiFetch } from '@/lib/api';

// ── Master agent ──────────────────────────────────────────────────────────────

// MarketOpportunity mirrors internal/master.MarketOpportunity (no json tags →
// PascalCase on the wire).
export interface MarketOpportunity {
  Pair: string;
  Exchange: string;
  OpportunityScore: number;
  Volatility: number;
  Volume24hUSD: number;
  TrendStrength: number;
  SentimentScore: number;
  BTCCorrelation: number;
  RegimeMatch: number;
  RecentLossPenalty: number;
  ProfitFactor: number;
  Reasoning: string;
  DetectedAt: string;
}

// MasterState mirrors internal/master.MasterState (no json tags → PascalCase).
export interface MasterState {
  Running: boolean;
  StartedAt: string;
  LastScanAt: string;
  LastScanDuration: number; // nanoseconds (Go time.Duration)
  ScanCount: number;
  ActiveBots: number;
  TotalCapitalUSD: number;
  AllocatedUSD: number;
  Opportunities: MarketOpportunity[] | null;
  Allocations: Record<string, unknown> | null;
  LastError: string;
  MultiPairEnabled: boolean;
  SpawnThreshold: number;
  // FleetActive is true when the canonical AI orchestrator is wired (the bot
  // fleet endpoints serve from it). Distinct from MultiPairEnabled, which
  // reflects the legacy scalping spawner.
  FleetActive?: boolean;
}

export interface PaperModeState {
  paper_mode: boolean;
  mode: string;
}

export interface OpportunitiesResult {
  total: number;
  last_scan_at: string;
  opportunities: MarketOpportunity[] | null;
}

/** GET /api/v1/master/status — snapshot of the master agent state. */
export async function getMasterStatus(): Promise<MasterState> {
  return apiFetch<MasterState>('/api/v1/master/status');
}

/** POST /api/v1/master/start — launch the master loop. 409 if already running. */
export async function startMaster(): Promise<{ message?: string }> {
  return apiFetch('/api/v1/master/start', { method: 'POST', body: JSON.stringify({}) });
}

/** POST /api/v1/master/stop — signal the master loop to stop. 409 if not running. */
export async function stopMaster(): Promise<{ message?: string }> {
  return apiFetch('/api/v1/master/stop', { method: 'POST', body: JSON.stringify({}) });
}

/** POST /api/v1/master/tick — run a single master cycle synchronously. */
export async function forceTick(): Promise<{ message?: string }> {
  return apiFetch('/api/v1/master/tick', { method: 'POST', body: JSON.stringify({}) });
}

/** GET /api/v1/master/opportunities — most recent scan opportunities. */
export async function getOpportunities(): Promise<OpportunitiesResult> {
  return apiFetch<OpportunitiesResult>('/api/v1/master/opportunities');
}

/** GET /api/v1/master/paper_mode — global spawner paper-mode state. */
export async function getMasterPaperMode(): Promise<PaperModeState> {
  return apiFetch<PaperModeState>('/api/v1/master/paper_mode');
}

/** POST /api/v1/master/paper_mode — toggle global spawner paper-mode (new bots only). */
export async function setMasterPaperMode(enabled: boolean): Promise<{ paper_mode: boolean; mode: string; warning?: string }> {
  return apiFetch('/api/v1/master/paper_mode', {
    method: 'POST',
    body: JSON.stringify({ enabled }),
  });
}

// ── Bots ──────────────────────────────────────────────────────────────────────

// BotInfo mirrors internal/scalping/manager.BotInfo (snake_case json tags).
export interface BotInfo {
  bot_id: string;
  pair: string;
  pairs: string[] | null;
  exchange: string;
  started_at: string;
  status: string;
  paper_mode: boolean;
  trades_today: number;
  pnl_today: number;
  total_trades: number;
  winning_trades: number;
  active_positions: number;
}

export interface ListBotsResult {
  total: number;
  bots: BotInfo[] | null;
}

/** GET /api/v1/bots — active bots from the BotManager. */
export async function listBots(): Promise<ListBotsResult> {
  return apiFetch<ListBotsResult>('/api/v1/bots');
}

/** POST /api/v1/bots/:pair/kill — stop a single running bot. */
export async function killBot(pair: string, reason = 'manual kill via motor screen'): Promise<{ message?: string }> {
  return apiFetch(`/api/v1/bots/${encodeURIComponent(pair)}/kill`, {
    method: 'POST',
    body: JSON.stringify({ reason }),
  });
}

/** POST /api/v1/bots/:pair/paper_mode — toggle paper mode on a single running bot. */
export async function setBotPaperMode(pair: string, enabled: boolean): Promise<{ pair: string; paper_mode: boolean; mode: string; warning?: string }> {
  return apiFetch(`/api/v1/bots/${encodeURIComponent(pair)}/paper_mode`, {
    method: 'POST',
    body: JSON.stringify({ enabled }),
  });
}

// ── Emergency stop (Level 4) ──────────────────────────────────────────────────

// EmergencyStopAllResponse mirrors handlers.EmergencyStopAllResponse.
export interface EmergencyStopAllResult {
  triggered_at: string;
  dry_run: boolean;
  active_bots_before: number;
  positions_closed: number;
  errors?: string[];
}

/**
 * POST /api/v1/admin/bots/emergency-stop-all — Level-4 kill of every active bot.
 * Pass dryRun=true to preview counts without stopping anything. The backend
 * enforces user_level >= 4 regardless of UI state.
 */
export async function emergencyStopAll(opts?: { dryRun?: boolean; reason?: string }): Promise<EmergencyStopAllResult> {
  return apiFetch<EmergencyStopAllResult>('/api/v1/admin/bots/emergency-stop-all', {
    method: 'POST',
    body: JSON.stringify({ dry_run: opts?.dryRun ?? false, reason: opts?.reason ?? '' }),
  });
}

// ── Portfolio overview (futures margin context) ───────────────────────────────

// Minimal typing of GET /api/v1/portfolio/overview (internal/gct
// PortfolioOverview) — the real payload is much larger; only the fields the
// UI consumes are declared here.
export interface PortfolioOverviewLite {
  summary: {
    total_equity_usd: number;
    available_for_trading_usd: number;
  };
  futures: {
    account_equity: number;
    available_balance: number;
  };
  /** Open futures positions (top-level `positions` in the backend payload). */
  positions?: PortfolioPositionLite[] | null;
}

export interface PortfolioPositionLite {
  symbol: string; // KuCoin contract, e.g. "QNTUSDTM"
  side: string;
  size: number;
  unrealized_pnl: number;
  notional_usd?: number;
}

/** GET /api/v1/portfolio/overview — spot+futures snapshot; used by the xsec
 * panel to show available futures margin next to the basket size inputs. */
export async function getPortfolioOverview(exchange = 'kucoin'): Promise<PortfolioOverviewLite> {
  return apiFetch<PortfolioOverviewLite>(`/api/v1/portfolio/overview?exchange=${encodeURIComponent(exchange)}`);
}

// ── XSec status (read-only) ───────────────────────────────────────────────────

export interface XSecEquityPoint {
  ts: string;
  equity: number;
  n_long: number;
  n_short: number;
}

// XSecRealEquityPoint — REAL account value over time (hourly snapshots from
// the actual KuCoin account, portfolio_snapshots table), NOT the xsec_equity
// paper curve above. This is the only equity series safe to chart on a
// live-money dashboard.
export interface XSecRealEquityPoint {
  ts: string;
  total_equity_usd: number;
}

export interface XSecPosition {
  pair: string;
  dir: number; // +1 long, -1 short
  entry: number;
  notional: number;
  opened_at: string;
  current_price?: number;
  unrealized_pnl?: number;
}

export interface XSecParam {
  key: string;
  value: string;
  updated_at: string;
}

export interface XSecShadowBasket {
  ts: string;
  payload: string;
}

// XSecTotals mirrors handlers.XSecTotals — the consolidated basket summary.
// total_unrealized_pnl/pct are absent (not 0) whenever known_legs is 0, or
// less than the basket's total leg count when the exchange enrichment
// missed one or more legs (a partial sum, not "flat").
export interface XSecTotals {
  n_long: number;
  n_short: number;
  total_notional: number;
  known_legs: number;
  total_unrealized_pnl?: number;
  total_unrealized_pnl_pct?: number;
}

/** One currently-firing basket-health alert (from the backend alert monitor). */
export interface XSecActiveAlert {
  key: string;
  title: string;
  body: string;
  fired_at: string;
}

export interface XSecStatus {
  equity_history: XSecEquityPoint[] | null;
  real_equity_history: XSecRealEquityPoint[] | null;
  positions: XSecPosition[] | null;
  totals: XSecTotals;
  params: XSecParam[] | null;
  shadow: XSecShadowBasket[] | null;
  active_alerts: XSecActiveAlert[] | null;
  updated_at: string;
}

/** GET /api/v1/xsec/status — read-only market-neutral xsec paper-bot state. */
export async function getXSecStatus(): Promise<XSecStatus> {
  return apiFetch<XSecStatus>('/api/v1/xsec/status');
}
