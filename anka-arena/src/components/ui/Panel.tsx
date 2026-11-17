import { type ReactNode } from 'react';
import clsx from 'clsx';

interface PanelProps {
  children: ReactNode;
  className?: string;
  inset?: boolean;
}

export function Panel({ children, className, inset = false }: PanelProps) {
  return (
    <div className={clsx(inset ? 'panel-inset' : 'panel', className)}>
      {children}
    </div>
  );
}

interface PanelHeaderProps {
  title: string;
  sub?: string;
  children?: ReactNode;
  className?: string;
}

export function PanelHeader({ title, sub, children, className }: PanelHeaderProps) {
  return (
    <div className={clsx('panel-h', className)}>
      <div style={{ flex: 1 }}>
        <h3>{title}</h3>
        {sub && <span className="sub">{sub}</span>}
      </div>
      {children}
    </div>
  );
}

interface PanelBodyProps {
  children: ReactNode;
  className?: string;
}

export function PanelBody({ children, className }: PanelBodyProps) {
  return <div className={clsx('panel-b', className)}>{children}</div>;
}
