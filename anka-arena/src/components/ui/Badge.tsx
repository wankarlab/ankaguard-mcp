import { type ReactNode, type CSSProperties } from 'react';
import clsx from 'clsx';

export type BadgeVariant = 'neutral' | 'ok' | 'paper' | 'micro' | 'real' | 'brand';

interface BadgeProps {
  variant?: BadgeVariant;
  dot?: boolean;
  dotColor?: string;
  children: ReactNode;
  className?: string;
  style?: CSSProperties;
}

export function Badge({
  variant = 'neutral',
  dot,
  dotColor,
  children,
  className,
  style,
}: BadgeProps) {
  return (
    <span className={clsx('badge', variant, className)} style={style}>
      {dot && (
        <span
          className="bd-dot"
          style={dotColor ? { background: dotColor } : undefined}
        />
      )}
      {children}
    </span>
  );
}

/**
 * Risk-tier badge: paper | micro | real
 */
export function TierBadge({ tier }: { tier: 'paper' | 'micro' | 'real' }) {
  const labels = { paper: 'PAPER', micro: 'MICRO', real: 'REAL' };
  return (
    <Badge variant={tier} dot>
      {labels[tier]}
    </Badge>
  );
}
