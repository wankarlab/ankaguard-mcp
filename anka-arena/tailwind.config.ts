import type { Config } from 'tailwindcss';
import { color, layout } from './src/design-system/tokens';

const config: Config = {
  content: ['./src/**/*.{js,ts,jsx,tsx,mdx}'],
  theme: {
    extend: {
      colors: {
        // Surfaces
        'bg-void':   color.bgVoid,
        'bg-base':   color.bgBase,
        'bg-panel':  color.bgPanel,
        'bg-panel2': color.bgPanel2,
        'bg-inset':  color.bgInset,
        'bg-hover':  color.bgHover,
        // Borders
        line:       color.line,
        'line-2':   color.line2,
        'line-glow': color.lineGlow,
        // Text
        'tx-hi':    color.txHi,
        tx:         color.tx,
        'tx-dim':   color.txDim,
        'tx-faint': color.txFaint,
        // Risk palette
        paper:    color.paper,
        micro:    color.micro,
        real:     color.real,
        // PnL
        up:   color.up,
        down: color.down,
        // Brand
        brand:  color.brand,
        brand2: color.brand2,
        cyan:   color.cyan,
        // Semantic
        ok:   color.ok,
        warn: color.warn,
        bad:  color.bad,
        info: color.info,
      },
      fontFamily: {
        sans: ['var(--font-ibm-sans)', 'system-ui', 'sans-serif'],
        mono: ['var(--font-ibm-mono)', 'ui-monospace', 'SF Mono', 'monospace'],
      },
      width: {
        rail: layout.railW,
      },
      height: {
        topbar: layout.topbarH,
      },
      borderRadius: {
        sm: '4px',
        DEFAULT: '7px',
        lg: '11px',
      },
    },
  },
  plugins: [],
};

export default config;
