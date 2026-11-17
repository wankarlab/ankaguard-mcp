import clsx from 'clsx';

interface MeterProps {
  /** 0–100 */
  value: number;
  /** Bar fill color (CSS color string or variable) */
  color?: string;
  className?: string;
  'aria-label'?: string;
}

export function Meter({
  value,
  color = 'var(--brand)',
  className,
  'aria-label': ariaLabel,
}: MeterProps) {
  const pct = Math.max(0, Math.min(100, value));

  return (
    <div
      className={clsx('meter', className)}
      role="meter"
      aria-valuenow={pct}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-label={ariaLabel}
    >
      <i style={{ width: `${pct}%`, background: color }} />
    </div>
  );
}
