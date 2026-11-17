import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import type { AgentToken, LicenseStatus } from '@/lib/product-api';

// Node environment without DOM: components render to static markup with next-intl
// mocked (keys are echoed back) and the data hooks mocked per case.
let licenseHook: { data?: LicenseStatus; error?: unknown; isLoading?: boolean } = {};
let pathname = '/home';

vi.mock('next-intl', () => ({
  useTranslations: () => (key: string, vals?: Record<string, unknown>) =>
    vals ? `${key}|${Object.values(vals).join(',')}` : key,
}));
vi.mock('@/i18n/routing', () => ({
  Link: ({ href, children }: { href: string; children: React.ReactNode }) => <a href={href}>{children}</a>,
  usePathname: () => pathname,
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
}));
vi.mock('swr', () => ({ default: () => ({ data: undefined, error: undefined, isLoading: false }), mutate: vi.fn() }));
vi.mock('@/lib/api', () => {
  class ApiError extends Error {
    constructor(public status: number, public body: unknown) { super(`HTTP ${status}`); }
  }
  return { ApiError, apiFetch: vi.fn(), api: {} };
});
vi.mock('./hooks', () => ({
  useLicense: () => ({ data: undefined, error: undefined, isLoading: false, ...licenseHook }),
  useProductStatus: () => ({ data: undefined, error: undefined }),
}));

import { ApiError } from '@/lib/api';
import { LiveModeIndicator } from './LiveModeBadge';
import { LicenseCard, LicenseExpiryWarning } from './LicenseParts';
import { LicenseBlocked, ProductGuard, needsSession } from './ProductGuard';
import { LoginLocked, PasswordNotSet, SET_PASSWORD_COMMAND } from './ProductLogin';
import { TokenCreatedNotice, TokenList } from './TokenParts';
import { SystemHealthView } from './SystemHealthScreen';

const lic = (over: Partial<LicenseStatus> = {}): LicenseStatus => ({
  edition: 'pro', state: 'valid', holder: 'ACME SA', license_id: 'L1', expires_at: '2027-01-31T00:00:00Z',
  days_remaining: 100, grace_days_remaining: 0, warning: null, ...over,
});
const tok = (over: Partial<AgentToken> = {}): AgentToken => ({
  id: 't1', name: 'my-agent', profile: 'paper', created_at: '2026-11-01T00:00:00Z',
  expires_at: '2099-01-01T00:00:00Z', last_used_at: null, revoked_at: null, ...over,
});

describe('LiveModeIndicator', () => {
  it.each([['live', 'mode_live'], ['off', 'mode_off'], ['unknown', 'mode_unknown']] as const)('%s', (mode, key) => {
    const html = renderToStaticMarkup(<LiveModeIndicator mode={mode} />);
    expect(html).toContain(`data-live-mode="${mode}"`);
    expect(html).toContain(key);
  });
});

describe('login pieces', () => {
  it('password not set shows the server command and no input', () => {
    const html = renderToStaticMarkup(<PasswordNotSet />);
    expect(html).toContain(SET_PASSWORD_COMMAND);
    expect(SET_PASSWORD_COMMAND).toBe('trading-mcp-server operator set-password');
    expect(html).not.toContain('<input');
  });

  it('lock message shows the wait from Retry-After', () => {
    expect(renderToStaticMarkup(<LoginLocked seconds={125} />)).toContain('2 min 5 s');
    expect(renderToStaticMarkup(<LoginLocked seconds={null} />)).toContain('login_locked_unknown');
  });
});

describe('license screen pieces', () => {
  it('shows edition, holder, expiry, days and state; never the document', () => {
    const html = renderToStaticMarkup(<LicenseCard status={lic()} />);
    for (const s of ['lic_edition_pro', 'ACME SA', '2027-01-31', '100', 'lic_state_valid']) expect(html).toContain(s);
    expect(html).not.toContain('<textarea');
  });

  it('missing values render as a dash, not as examples', () => {
    const html = renderToStaticMarkup(<LicenseCard status={lic({ edition: 'community', state: 'invalid', holder: null, expires_at: null, days_remaining: null })} />);
    expect(html).toContain('—');
    expect(html).not.toContain('ACME');
  });

  it('warns at 14 days or fewer with a link on home, silent above', () => {
    const near = renderToStaticMarkup(<LicenseExpiryWarning status={lic({ days_remaining: 14 })} withLink />);
    expect(near).toContain('expiry_text|14');
    expect(near).toContain('href="/license"');
    expect(renderToStaticMarkup(<LicenseExpiryWarning status={lic({ days_remaining: 15 })} />)).toBe('');
    expect(renderToStaticMarkup(<LicenseExpiryWarning status={undefined} />)).toBe('');
  });
});

describe('ProductGuard', () => {
  beforeEach(() => { vi.stubEnv('NEXT_PUBLIC_EDITION', 'product'); licenseHook = {}; pathname = '/control-panel'; });
  afterEach(() => vi.unstubAllEnvs());
  const render = () => renderToStaticMarkup(<ProductGuard><p>CONTENT</p></ProductGuard>);

  it('shows the screen with a valid Pro license', () => {
    licenseHook = { data: lic() };
    expect(render()).toContain('CONTENT');
  });

  it('blocks with the reason and a link to the license without Pro', () => {
    licenseHook = { data: lic({ edition: 'community', state: 'invalid' }) };
    const html = render();
    expect(html).not.toContain('CONTENT');
    expect(html).toContain('blocked_invalid');
    expect(html).toContain('href="/license"');
  });

  it('stays blocked when the license cannot be checked', () => {
    licenseHook = { error: new ApiError(500, {}) };
    const html = render();
    expect(html).not.toContain('CONTENT');
    expect(html).toContain('blocked_unknown');
    expect(render()).toContain('500');
  });

  it('asks to sign in on 401', () => {
    licenseHook = { error: new ApiError(401, {}) };
    expect(render()).toContain('blocked_session');
  });

  it.each(['/login', '/system-health', '/license'])('lets %s through without a license', (p) => {
    pathname = p;
    licenseHook = { data: lic({ edition: 'community', state: 'invalid' }) };
    expect(render()).toContain('CONTENT');
  });

  it('does nothing outside the product edition', () => {
    vi.stubEnv('NEXT_PUBLIC_EDITION', '');
    licenseHook = { data: lic({ edition: 'community', state: 'invalid' }) };
    expect(render()).toBe('<p>CONTENT</p>');
  });

  it('requires a session on every screen but the login', () => {
    expect(needsSession('/license', false)).toBe(true);
    expect(needsSession('/system-health', false)).toBe(true);
    expect(needsSession('/control-panel', false)).toBe(true);
    expect(needsSession('/login', false)).toBe(false);
    expect(needsSession('/en/login', false)).toBe(false);
    expect(needsSession('/license', true)).toBe(false);
  });

  it('LicenseBlocked mentions the emergency stop', () => {
    expect(renderToStaticMarkup(<LicenseBlocked kind="none" />)).toContain('blocked_estop');
  });
});

describe('token screen pieces', () => {
  it('created notice shows the value once with copy and the warning', () => {
    const html = renderToStaticMarkup(<TokenCreatedNotice value="tmcp_secret_value" onDismiss={() => {}} />);
    expect(html).toContain('tmcp_secret_value');
    expect(html).toContain('tok_created_warning');
    expect(html).toContain('tok_copy');
  });

  const handlers = { onAsk: () => {}, onConfirm: () => {}, onCancel: () => {} };

  it('lists tokens without values and revoked ones as inactive', () => {
    const html = renderToStaticMarkup(
      <TokenList tokens={[tok(), tok({ id: 't2', name: 'old', revoked_at: '2026-11-02T00:00:00Z' })]} pendingId={null} {...handlers} />,
    );
    expect(html).toContain('my-agent');
    expect(html).toContain('tok_revoked');
    expect(html).not.toContain('tmcp_');
    // only the active token has a revoke button
    expect(html.match(/tok_revoke</g)).toHaveLength(1);
    expect(html).not.toContain('alertdialog');
  });

  it('shows the inline confirmation only for the pending token', () => {
    const html = renderToStaticMarkup(<TokenList tokens={[tok(), tok({ id: 't2', name: 'other' })]} pendingId="t2" {...handlers} />);
    expect(html).toContain('alertdialog');
    expect(html).toContain('tok_revoke_confirm|other');
    expect(html).toContain('tok_revoke_yes');
    expect(html).toContain('tok_revoke_no');
    expect(html.match(/alertdialog/g)).toHaveLength(1);
  });

  it('empty list shows a message', () => {
    expect(renderToStaticMarkup(<TokenList tokens={[]} pendingId={null} {...handlers} />)).toContain('tok_empty');
  });
});

describe('SystemHealthView', () => {
  it('lists every component with its message', () => {
    const html = renderToStaticMarkup(
      <SystemHealthView health={{ status: 'degraded', engine_real_money: null, checked_at: '2026-11-16T10:00:00Z', components: [
        { name: 'gct', critical: true, status: 'ok', latency_ms: 12, message: 'connected' },
        { name: 'ml', critical: false, status: 'down', latency_ms: 2000, message: 'timeout' },
      ] }} />,
    );
    for (const s of ['health_state_degraded', 'gct', 'connected', 'ml', 'timeout']) expect(html).toContain(s);
  });
});
