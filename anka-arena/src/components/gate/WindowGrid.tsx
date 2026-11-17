// WindowGrid.tsx — Grid de 10 ventanas walk-forward OOS
// Cada celda se revela una por una con transición de opacidad.

import { useTranslations } from 'next-intl';

interface WindowGridProps {
  /** true = ventana verde (OOS ganadora), false = roja */
  windows: boolean[];
  /** Cuántas ventanas están ya reveladas (0..windows.length) */
  revealed: number;
}

export function WindowGrid({ windows, revealed }: WindowGridProps) {
  const t = useTranslations('UiGate');
  return (
    <div>
      <div className="eyebrow" style={{ marginBottom: 8 }}>
        {t('walk_forward_windows')}
      </div>
      <div className="wf-row" style={{ gap: 5, marginBottom: 4, flexWrap: 'wrap' }}>
        {windows.map((win, i) => {
          const isRevealed = i < revealed;
          return (
            <div
              key={i}
              className={
                'wf-cell ' +
                (isRevealed ? (win ? 'wf-green' : 'wf-red') : '')
              }
              style={{
                width: 40,
                height: 40,
                fontSize: 14,
                background: isRevealed ? undefined : 'var(--bg-inset)',
                borderColor: isRevealed ? undefined : 'var(--line)',
                border: isRevealed ? undefined : '1px solid var(--line)',
                transition: 'all 0.2s',
                opacity: isRevealed ? 1 : 0.4,
              }}
            >
              {isRevealed ? (win ? '▲' : '▾') : ''}
            </div>
          );
        })}
      </div>
      <div className="row" style={{ gap: 4, marginTop: 6 }}>
        <span className="mono faint" style={{ fontSize: 9.5 }}>
          {t('folds_legend')}
        </span>
      </div>
    </div>
  );
}
