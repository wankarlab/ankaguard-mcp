// adapters.test.ts — smoke tests for the envelope unwrap + snake_case→camelCase
// adapters. These are pure functions (no DOM, no network), so they are the
// highest-value, lowest-risk thing to lock down: they guard the {success,data}
// contract and the field-name bridge that every typed fetcher relies on.

import { describe, it, expect } from 'vitest';
import {
  unwrap,
  toEngineStats,
  toGuildStrategies,
  toFeeLedger,
  toExchanges,
} from '@/lib/adapters';

describe('unwrap (envelope)', () => {
  it('returns data when wrapped in {success,data,timestamp}', () => {
    const env = { success: true, data: { foo: 1 }, timestamp: 'now' };
    expect(unwrap(env)).toEqual({ foo: 1 });
  });

  it('passes through a bare object that has no data field', () => {
    const raw = { foo: 1 };
    expect(unwrap(raw)).toEqual({ foo: 1 });
  });

  it('passes through a bare array (no data field)', () => {
    const raw = [1, 2, 3];
    expect(unwrap(raw)).toEqual([1, 2, 3]);
  });

  it('returns the (undefined) data value when data is explicitly present but empty', () => {
    expect(unwrap({ success: true, data: null })).toBeNull();
  });
});

describe('toEngineStats', () => {
  it('maps backend snake_case fields to the camelCase EngineStats partial', () => {
    const res = {
      success: true,
      data: {
        edges_total: 119,
        edges_live: 7,
        gate_pass_rate: 42.5,
        gbm_pf_oos: 2.61,
        aum_cert: 1500,
        feature_store_rows: 2412345,
        specs_in_queue: 14,
        specs_certified_today: 3,
      },
    };
    expect(toEngineStats(res)).toEqual({
      edgesTotal: 119,
      edgesCertified: 7,
      gatePassPct: 42.5,
      gbmPf: 2.61,
      aumCertUsd: 1500,
      featureStoreRows: 2412345,
      specsInQueue: 14,
      specsCertifiedToday: 3,
    });
  });

  it('defaults every field to 0 when the payload is empty (no hardcoded business values)', () => {
    expect(toEngineStats({ success: true, data: {} })).toEqual({
      edgesTotal: 0,
      edgesCertified: 0,
      gatePassPct: 0,
      gbmPf: 0,
      aumCertUsd: 0,
      featureStoreRows: 0,
      specsInQueue: 0,
      specsCertifiedToday: 0,
    });
  });
});

describe('toGuildStrategies', () => {
  it('maps strategies, converts consistency 0–1 → percent, derives kind/regime/copiable', () => {
    const res = {
      data: {
        strategies: [
          {
            spec_id: 's1',
            author: 'AnkaAI-bot',
            regime: 'trending',
            pf_oos: 1.5,
            consistency: 0.72,
            max_drawdown: -0.3,
            days_active: 10,
            copiers: 4,
            status: 'certified',
          },
          {
            spec_id: 's2',
            author: 'alice',
            regime: 'weird',
            status: 'retired',
            retired: true,
          },
        ],
      },
    };
    const out = toGuildStrategies(res);
    expect(out).toHaveLength(2);
    expect(out[0]).toMatchObject({
      id: 's1',
      kind: 'ai',
      regime: 'TRENDING',
      consistency_pct: 72,
      copiable: true,
    });
    expect(out[1]).toMatchObject({
      id: 's2',
      kind: 'human',
      regime: 'UNKNOWN', // unknown regime falls back, never invented
      retired: true,
      copiable: false,
    });
  });

  it('returns an empty array when there are no strategies', () => {
    expect(toGuildStrategies({ data: { strategies: [] } })).toEqual([]);
    expect(toGuildStrategies({ data: {} })).toEqual([]);
  });
});

describe('toFeeLedger (economy is backend-sourced, never hardcoded)', () => {
  it('maps economy splits + aggregates from the backend, defaulting to 0 when absent', () => {
    const res = {
      data: {
        user_id: 'u1',
        periods: [
          {
            id: 'p1',
            user_id: 'u1',
            fee_usd: 100,
            author_amount: 70,
            treasury_amount: 30,
            rate: 0.2,
            status: 'open',
          },
        ],
        economy: { fee_rate: 0.2, author_split: 0.7, treasury_split: 0.3 },
        aggregate: {
          total_fee_usd: 100,
          total_author_amount: 70,
          total_treasury_amount: 30,
        },
      },
    };
    const out = toFeeLedger(res);
    expect(out.economy).toEqual({ feeRate: 0.2, authorSplit: 0.7, treasurySplit: 0.3 });
    expect(out.aggregate).toEqual({
      totalFeeUsd: 100,
      totalAuthorAmount: 70,
      totalTreasuryAmount: 30,
    });
    expect(out.periods[0]).toMatchObject({ id: 'p1', feeUsd: 100, authorAmount: 70 });
  });

  it('returns zeroed economy (no client-side default 20%/70-30) when missing', () => {
    const out = toFeeLedger({ data: {} });
    expect(out.economy).toEqual({ feeRate: 0, authorSplit: 0, treasurySplit: 0 });
    expect(out.periods).toEqual([]);
  });
});

describe('toExchanges', () => {
  it('derives connected/enabled from status=active', () => {
    const res = { data: { exchanges: [{ name: 'kucoin', status: 'active' }] } };
    expect(toExchanges(res)).toEqual([
      { name: 'kucoin', enabled: true, sandbox: false, connected: true },
    ]);
  });
});
