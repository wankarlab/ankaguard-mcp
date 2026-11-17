import { useTranslations } from 'next-intl';
import { isProductEdition } from '@/lib/productMode';
import type { Ring } from './types';

export function Ring1Confirm({
  ring,
  specId,
  setSpecId,
  onCancel,
  onConfirm,
  loading,
}: {
  ring: Ring;
  specId: string;
  setSpecId: (v: string) => void;
  onCancel: () => void;
  onConfirm: () => void;
  loading: boolean;
}) {
  const t = useTranslations('KillSwitch');
  return (
    <div>
      <div
        className="mono"
        style={{ color: ring.color, fontSize: 11, letterSpacing: '.08em', marginBottom: 10 }}
      >
        {t(`ring${ring.id}_name`)}
      </div>
      <p style={{ fontSize: 12.5, color: 'var(--tx)', marginBottom: 12 }}>
        {t('ring1_confirm_desc')}
      </p>
      <label style={{ display: 'block', marginBottom: 6 }}>
        <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>Spec ID</span>
        <input
          autoFocus
          value={specId}
          onChange={(e) => setSpecId(e.target.value)}
          placeholder="mean-revert-ranging-v3"
          style={{
            display: 'block',
            width: '100%',
            marginTop: 4,
            background: 'var(--bg-void)',
            border: '1px solid var(--line)',
            borderRadius: 6,
            padding: '8px 10px',
            color: 'var(--tx-hi)',
            fontFamily: 'var(--mono)',
            fontSize: 12,
          }}
        />
      </label>
      <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
        <button className="btn ghost" style={{ flex: 1 }} onClick={onCancel} disabled={loading}>
          {t('cancel')}
        </button>
        <button
          className="btn danger"
          style={{ flex: 1 }}
          disabled={!specId.trim() || loading}
          onClick={onConfirm}
        >
          {loading ? t('freezing') : t('freeze_spec')}
        </button>
      </div>
    </div>
  );
}

export function Ring2Confirm({
  ring,
  tenantId,
  setTenantId,
  onCancel,
  onConfirm,
  loading,
}: {
  ring: Ring;
  tenantId: string;
  setTenantId: (v: string) => void;
  onCancel: () => void;
  onConfirm: () => void;
  loading: boolean;
}) {
  const t = useTranslations('KillSwitch');
  return (
    <div>
      <div
        className="mono"
        style={{ color: ring.color, fontSize: 11, letterSpacing: '.08em', marginBottom: 10 }}
      >
        {t(`ring${ring.id}_name`)}
      </div>
      <p style={{ fontSize: 12.5, color: 'var(--tx)', marginBottom: 12 }}>
        {t('ring2_confirm_desc')}
      </p>
      <label style={{ display: 'block', marginBottom: 6 }}>
        <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>Tenant ID</span>
        <input
          autoFocus
          value={tenantId}
          onChange={(e) => setTenantId(e.target.value)}
          placeholder="u_4471"
          style={{
            display: 'block',
            width: '100%',
            marginTop: 4,
            background: 'var(--bg-void)',
            border: '1px solid var(--line)',
            borderRadius: 6,
            padding: '8px 10px',
            color: 'var(--tx-hi)',
            fontFamily: 'var(--mono)',
            fontSize: 12,
          }}
        />
      </label>
      <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
        <button className="btn ghost" style={{ flex: 1 }} onClick={onCancel} disabled={loading}>
          {t('cancel')}
        </button>
        <button
          className="btn danger"
          style={{ flex: 1 }}
          disabled={!tenantId.trim() || loading}
          onClick={onConfirm}
        >
          {loading ? t('isolating') : t('isolate_tenant')}
        </button>
      </div>
    </div>
  );
}

export function Ring0Confirm({
  ring,
  confirm,
  setConfirm,
  onCancel,
  onConfirm,
  loading,
}: {
  ring: Ring;
  confirm: string;
  setConfirm: (v: string) => void;
  onCancel: () => void;
  onConfirm: () => void;
  loading: boolean;
}) {
  const t = useTranslations('KillSwitch');
  const isValid = confirm === 'CERRAR TODO';
  const product = isProductEdition();
  return (
    <div>
      <div
        className="mono"
        style={{ color: ring.color, fontSize: 11, letterSpacing: '.08em', marginBottom: 10 }}
      >
        {product ? t('product_stop_name') : t(`ring${ring.id}_name`)}
      </div>
      <p style={{ fontSize: 12.5, color: 'var(--tx)', marginBottom: 8 }}>
        {t('nuclear_prefix')}{' '}
        <strong style={{ color: ring.color }}>{t(product ? 'product_nuclear_close_all' : 'nuclear_close_all')}</strong>{t('nuclear_type')}{' '}
        <strong className="mono" style={{ color: 'var(--tx-hi)' }}>
          CERRAR TODO
        </strong>{' '}
        {t('to_confirm')}
      </p>
      <input
        autoFocus
        value={confirm}
        onChange={(e) => setConfirm(e.target.value)}
        placeholder="CERRAR TODO"
        aria-label={t('type_confirm_aria')}
        style={{
          width: '100%',
          background: 'var(--bg-void)',
          border: `1px solid ${ring.color}`,
          borderRadius: 6,
          padding: '9px 11px',
          color: 'var(--tx-hi)',
          fontFamily: 'var(--mono)',
          letterSpacing: '.1em',
          fontSize: 13,
          marginTop: 4,
        }}
      />
      {confirm.length > 0 && !isValid && (
        <div className="mono" style={{ fontSize: 10, color: ring.color, marginTop: 5 }}>
          {t('type_exactly')}
        </div>
      )}
      <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
        <button className="btn ghost" style={{ flex: 1 }} onClick={onCancel} disabled={loading}>
          {t('cancel')}
        </button>
        <button
          className="btn danger"
          style={{ flex: 1 }}
          disabled={!isValid || loading}
          onClick={onConfirm}
        >
          {loading ? t('executing') : t('close_all_btn')}
        </button>
      </div>
    </div>
  );
}

export function DonePanel({ ring, onClose }: { ring: Ring; onClose: () => void }) {
  const t = useTranslations('KillSwitch');
  return (
    <div style={{ textAlign: 'center', paddingTop: 24 }}>
      <div
        style={{
          width: 70,
          height: 70,
          margin: '0 auto 16px',
          borderRadius: '50%',
          background: `${ring.color}22`,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
        }}
      >
        <svg
          width="34"
          height="34"
          viewBox="0 0 24 24"
          fill="none"
          stroke={ring.color}
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
        >
          <polyline points="20 6 9 17 4 12" />
        </svg>
      </div>
      <div style={{ fontSize: 15, fontWeight: 600, color: 'var(--tx-hi)' }}>
        {t('triggered', { name: t(`ring${ring.id}_short`) })}
      </div>
      <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginTop: 4 }}>
        {t('stopped', { scope: t(`ring${ring.id}_scope`) })}
      </div>
      <div
        className="mono"
        style={{ fontSize: 10, color: '#4adb84', marginTop: 14 }}
      >
        ✓ {t('signed_audit')} · {new Date().toLocaleTimeString()}
      </div>
      <button className="btn" style={{ marginTop: 18 }} onClick={onClose}>
        {t('close_console')}
      </button>
    </div>
  );
}

export function ErrorPanel({
  ring,
  message,
  onRetry,
  onCancel,
}: {
  ring: Ring;
  message: string;
  onRetry: () => void;
  onCancel: () => void;
}) {
  const t = useTranslations('KillSwitch');
  return (
    <div style={{ textAlign: 'center', paddingTop: 24 }}>
      <div
        style={{
          width: 70,
          height: 70,
          margin: '0 auto 16px',
          borderRadius: '50%',
          background: `${ring.color}22`,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
        }}
      >
        <svg
          width="34"
          height="34"
          viewBox="0 0 24 24"
          fill="none"
          stroke={ring.color}
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
        >
          <circle cx="12" cy="12" r="10" />
          <line x1="12" y1="8" x2="12" y2="12" />
          <line x1="12" y1="16" x2="12.01" y2="16" />
        </svg>
      </div>
      <div style={{ fontSize: 14, fontWeight: 600, color: ring.color }}>{t('exec_error')}</div>
      <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginTop: 6, maxWidth: 220, margin: '8px auto 0' }}>
        {message}
      </div>
      <div style={{ display: 'flex', gap: 8, marginTop: 18, justifyContent: 'center' }}>
        <button className="btn ghost sm" onClick={onCancel}>
          {t('cancel')}
        </button>
        <button className="btn sm" onClick={onRetry}>
          {t('retry')}
        </button>
      </div>
    </div>
  );
}
