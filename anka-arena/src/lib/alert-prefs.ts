// alert-prefs.ts — pure helpers for the 9 xsec.alert.* params: parsing the raw
// string rows from /xsec/params and client-side range validation (D8).
// Backend defaults mirror loadThresholds() in xsec_alert_monitor.go and are only
// used to prefill keys that are not stored yet.

export const ALERT_KEYS = {
  enabled: 'xsec.alert.enabled',
  imbalancePct: 'xsec.alert.imbalance_pct',
  rebalanceDays: 'xsec.alert.rebalance_days',
  legPnlPct: 'xsec.alert.leg_pnl_pct',
  intervalMin: 'xsec.alert.interval_min',
  onImbalance: 'xsec.alert.on_imbalance',
  onStaleness: 'xsec.alert.on_staleness',
  onLegBlowout: 'xsec.alert.on_leg_blowout',
  onJobFailure: 'xsec.alert.on_job_failure',
} as const;

export type AlertField = keyof typeof ALERT_KEYS;

/** Form state: numbers kept as text so the user can type freely. */
export interface AlertForm {
  enabled: boolean;
  imbalancePct: string;
  rebalanceDays: string;
  legPnlPct: string;
  intervalMin: string;
  onImbalance: boolean;
  onStaleness: boolean;
  onLegBlowout: boolean;
  onJobFailure: boolean;
}

export const ALERT_DEFAULTS: AlertForm = {
  enabled: true,
  imbalancePct: '15',
  rebalanceDays: '10',
  legPnlPct: '-15',
  intervalMin: '5',
  onImbalance: true,
  onStaleness: true,
  onLegBlowout: true,
  onJobFailure: true,
};

export const NUMERIC_FIELDS = ['imbalancePct', 'rebalanceDays', 'legPnlPct', 'intervalMin'] as const;
export const BOOL_FIELDS = ['enabled', 'onImbalance', 'onStaleness', 'onLegBlowout', 'onJobFailure'] as const;
export type NumericField = (typeof NUMERIC_FIELDS)[number];
export type BoolField = (typeof BOOL_FIELDS)[number];

export const ALERT_KEY_LIST: string[] = Object.values(ALERT_KEYS);

/** Builds the form from raw param rows; reports which keys were absent. */
export function parseAlertParams(rows: { key: string; value: string }[]): { form: AlertForm; missing: AlertField[] } {
  const map = new Map(rows.map((r) => [r.key, r.value]));
  const form: AlertForm = { ...ALERT_DEFAULTS };
  const missing: AlertField[] = [];
  (Object.keys(ALERT_KEYS) as AlertField[]).forEach((f) => {
    const raw = map.get(ALERT_KEYS[f]);
    if (raw === undefined) {
      missing.push(f);
      return;
    }
    if ((BOOL_FIELDS as readonly string[]).includes(f)) {
      const v = raw.trim().toLowerCase();
      (form as unknown as Record<string, boolean>)[f] = v === 'true' || v === '1';
    } else {
      (form as unknown as Record<string, string>)[f] = raw.trim();
    }
  });
  return { form, missing };
}

export type AlertErrorCode = 'not_a_number' | 'negative' | 'min_interval' | 'min_days' | 'positive_loss';

/** Returns an error code per invalid numeric field (empty object = valid). */
export function validateAlertForm(form: AlertForm): Partial<Record<NumericField, AlertErrorCode>> {
  const errors: Partial<Record<NumericField, AlertErrorCode>> = {};
  const num = (s: string) => (s.trim() === '' ? NaN : Number(s));

  const imb = num(form.imbalancePct);
  if (!Number.isFinite(imb)) errors.imbalancePct = 'not_a_number';
  else if (imb < 0) errors.imbalancePct = 'negative';

  const days = num(form.rebalanceDays);
  if (!Number.isFinite(days)) errors.rebalanceDays = 'not_a_number';
  else if (days < 1) errors.rebalanceDays = 'min_days';

  // The leg threshold is a LOSS level (backend default -15): a positive value
  // would alert on profitable legs, so only <= 0 is accepted.
  const leg = num(form.legPnlPct);
  if (!Number.isFinite(leg)) errors.legPnlPct = 'not_a_number';
  else if (leg > 0) errors.legPnlPct = 'positive_loss';

  const interval = num(form.intervalMin);
  if (!Number.isFinite(interval)) errors.intervalMin = 'not_a_number';
  else if (interval < 1) errors.intervalMin = 'min_interval';

  return errors;
}

/** Keys whose serialized value differs between two forms, as [key, value] pairs. */
export function diffAlertForms(base: AlertForm, next: AlertForm): [string, string][] {
  const out: [string, string][] = [];
  (Object.keys(ALERT_KEYS) as AlertField[]).forEach((f) => {
    const a = base[f];
    const b = next[f];
    const changed = typeof a === 'boolean' ? a !== b : String(a).trim() !== String(b).trim();
    if (changed) out.push([ALERT_KEYS[f], typeof b === 'boolean' ? String(b) : String(b).trim()]);
  });
  return out;
}
