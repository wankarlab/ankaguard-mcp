import { describe, expect, it } from 'vitest';
import { ROLE_ORDER, ROLE_PERMISSIONS } from './rbac';

describe('rbac monotonicity', () => {
  it('each level includes every screen of the lower level', () => {
    for (let i = 1; i < ROLE_ORDER.length; i++) {
      const lower = ROLE_PERMISSIONS[ROLE_ORDER[i - 1]].allow;
      const upper = ROLE_PERMISSIONS[ROLE_ORDER[i]].allow;
      const missing = lower.filter((s) => !upper.includes(s));
      expect(missing, `${ROLE_ORDER[i]} misses ${missing.join(',')}`).toEqual([]);
    }
  });
  it('admin includes every role', () => {
    const admin = ROLE_PERMISSIONS.admin.allow;
    for (const r of ROLE_ORDER) {
      expect(ROLE_PERMISSIONS[r].allow.filter((s) => !admin.includes(s))).toEqual([]);
    }
  });
});
