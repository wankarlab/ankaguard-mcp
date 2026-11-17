import { describe, it, expect } from 'vitest';
import {
  ALERT_DEFAULTS,
  ALERT_KEY_LIST,
  diffAlertForms,
  parseAlertParams,
  validateAlertForm,
} from '@/lib/alert-prefs';

describe('validateAlertForm', () => {
  it('accepts the backend defaults', () => {
    expect(validateAlertForm(ALERT_DEFAULTS)).toEqual({});
  });
  it('rejects interval below 1 minute', () => {
    expect(validateAlertForm({ ...ALERT_DEFAULTS, intervalMin: '0' }).intervalMin).toBe('min_interval');
    expect(validateAlertForm({ ...ALERT_DEFAULTS, intervalMin: '1' }).intervalMin).toBeUndefined();
  });
  it('rejects days below 1 and negative imbalance', () => {
    expect(validateAlertForm({ ...ALERT_DEFAULTS, rebalanceDays: '0.5' }).rebalanceDays).toBe('min_days');
    expect(validateAlertForm({ ...ALERT_DEFAULTS, imbalancePct: '-1' }).imbalancePct).toBe('negative');
    expect(validateAlertForm({ ...ALERT_DEFAULTS, imbalancePct: '0' }).imbalancePct).toBeUndefined();
  });
  it('rejects empty or non-numeric values', () => {
    expect(validateAlertForm({ ...ALERT_DEFAULTS, imbalancePct: '' }).imbalancePct).toBe('not_a_number');
    expect(validateAlertForm({ ...ALERT_DEFAULTS, legPnlPct: 'abc' }).legPnlPct).toBe('not_a_number');
  });
  it('requires the leg threshold to be a loss level (<= 0)', () => {
    expect(validateAlertForm({ ...ALERT_DEFAULTS, legPnlPct: '5' }).legPnlPct).toBe('positive_loss');
    expect(validateAlertForm({ ...ALERT_DEFAULTS, legPnlPct: '-20' }).legPnlPct).toBeUndefined();
  });
});

describe('parseAlertParams', () => {
  it('reads stored values and reports missing keys', () => {
    const { form, missing } = parseAlertParams([
      { key: 'xsec.alert.imbalance_pct', value: '20' },
      { key: 'xsec.alert.enabled', value: 'false' },
    ]);
    expect(form.imbalancePct).toBe('20');
    expect(form.enabled).toBe(false);
    expect(missing).toContain('intervalMin');
    expect(missing).not.toContain('enabled');
    expect(form.intervalMin).toBe(ALERT_DEFAULTS.intervalMin);
  });
  it('exposes exactly 9 keys', () => {
    expect(ALERT_KEY_LIST).toHaveLength(9);
  });
});

describe('diffAlertForms', () => {
  it('returns only changed keys serialized as strings', () => {
    const next = { ...ALERT_DEFAULTS, imbalancePct: ' 25 ', onLegBlowout: false };
    expect(diffAlertForms(ALERT_DEFAULTS, next)).toEqual([
      ['xsec.alert.imbalance_pct', '25'],
      ['xsec.alert.on_leg_blowout', 'false'],
    ]);
  });
});
