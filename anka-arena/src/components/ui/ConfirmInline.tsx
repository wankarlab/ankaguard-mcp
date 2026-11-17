'use client';

// ConfirmInline — small inline two-step confirmation (message + confirm/cancel).
// Used instead of window.confirm, which is unreliable in the embedded viewer.

interface Props {
  message: string;
  confirmLabel: string;
  cancelLabel: string;
  onConfirm: () => void;
  onCancel: () => void;
  busy?: boolean;
}

export function ConfirmInline({ message, confirmLabel, cancelLabel, onConfirm, onCancel, busy }: Props) {
  return (
    <div
      role="alertdialog"
      aria-live="assertive"
      style={{
        display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap',
        padding: '10px 12px', borderRadius: 'var(--r)',
        border: '1px solid var(--down)', background: 'rgba(233,90,90,.07)',
      }}
    >
      <span style={{ fontSize: 12, color: 'var(--down)', fontWeight: 600, flex: '1 1 220px', lineHeight: 1.45 }}>
        {message}
      </span>
      <button
        type="button"
        disabled={busy}
        onClick={onConfirm}
        style={{ padding: '6px 12px', borderRadius: 'var(--r)', border: 'none', background: 'var(--down)', color: '#fff', fontSize: 12, fontWeight: 700, cursor: busy ? 'not-allowed' : 'pointer' }}
      >
        {confirmLabel}
      </button>
      <button
        type="button"
        onClick={onCancel}
        style={{ padding: '6px 12px', borderRadius: 'var(--r)', border: '1px solid var(--line)', background: 'transparent', color: 'var(--tx-dim)', fontSize: 12, cursor: 'pointer' }}
      >
        {cancelLabel}
      </button>
    </div>
  );
}
