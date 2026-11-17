import { useTranslations } from 'next-intl';
import type { Ring, RingId } from './types';
import { RINGS } from './types';

export function ConcentricRings({
  selectedId,
  onSelect,
}: {
  selectedId: RingId | null;
  onSelect: (r: Ring) => void;
}) {
  const t = useTranslations('KillSwitch');
  // Render order: outer rings first so inner rings are clickable on top
  const renderOrder = [RINGS[2], RINGS[1], RINGS[0]] as Ring[]; // id 0, 2, 1 by radius desc

  return (
    <svg viewBox="0 0 340 340" style={{ width: '100%', maxWidth: 300 }}>
      <defs>
        <style>{`
          @keyframes ks-ring0-pulse {
            0%   { r: 152; opacity: .5; }
            50%  { r: 160; opacity: 0; }
            100% { r: 152; opacity: .5; }
          }
        `}</style>
      </defs>

      {renderOrder.map((ring) => {
        const active = selectedId === ring.id;
        return (
          <g
            key={ring.id}
            style={{ cursor: 'pointer' }}
            role="button"
            aria-label={t(`ring${ring.id}_name`)}
            onClick={() => onSelect(ring)}
          >
            {/* Fill layer */}
            <circle
              cx="170"
              cy="170"
              r={ring.r}
              fill={ring.color}
              fillOpacity={active ? 0.12 : 0}
              stroke={ring.color}
              strokeWidth={active ? 2.5 : 1.6}
              strokeOpacity={active ? 1 : 0.55}
            />
            {/* Pulse ring for Ring 0 */}
            {ring.id === 0 && (
              <circle
                cx="170"
                cy="170"
                r={ring.r}
                fill="none"
                stroke={ring.color}
                strokeWidth="2"
                opacity="0.4"
              >
                <animate
                  attributeName="r"
                  values={`${ring.r};${ring.r + 8};${ring.r}`}
                  dur="2.2s"
                  repeatCount="indefinite"
                />
                <animate
                  attributeName="opacity"
                  values=".5;0;.5"
                  dur="2.2s"
                  repeatCount="indefinite"
                />
              </circle>
            )}
            {/* Label */}
            <text
              x="170"
              y={170 - ring.r + 15}
              textAnchor="middle"
              style={{
                fontFamily: 'var(--mono)',
                fontSize: 9,
                fill: ring.color,
                letterSpacing: '.06em',
                pointerEvents: 'none',
              }}
            >
              {t(`ring${ring.id}_short`)}
            </text>
          </g>
        );
      })}

      {/* Center STOP button — triggers Ring 0 */}
      <g
        style={{ cursor: 'pointer' }}
        role="button"
        aria-label={t('stop_global_aria')}
        onClick={() => onSelect(RINGS[2])}
      >
        <circle cx="170" cy="170" r="32" fill="#f0564f22" stroke="#f0564f" strokeWidth="2" />
        <path
          d="M170 155 v14 M162.5 158.5 a10 10 0 1 0 15 0"
          stroke="#f0564f"
          strokeWidth="2"
          strokeLinecap="round"
          fill="none"
        />
        <text
          x="170"
          y="188"
          textAnchor="middle"
          style={{
            fontFamily: 'var(--mono)',
            fontSize: 8,
            fill: '#f0564f',
            pointerEvents: 'none',
          }}
        >
          STOP
        </text>
      </g>
    </svg>
  );
}
