// AnkaArena — Design tokens (single source of truth)
// Palette: AnkaConsole dark terminal aesthetic
// Rule: COLOR = RISK (paper=blue, micro=amber, real=red)

export const color = {
  // Surfaces (near-black, cold tint)
  bgVoid:    '#06080c',
  bgBase:    '#0a0d13',
  bgPanel:   '#10141c',
  bgPanel2:  '#161b25',
  bgInset:   '#0c0f15',
  bgHover:   '#1b212d',

  // Borders
  line:      '#1c2330',
  line2:     '#283140',
  lineGlow:  '#323d4f',

  // Text
  txHi:    '#eef1f6',
  tx:      '#b9c0cd',
  txDim:   '#7e8799',
  txFaint: '#525b6c',

  // COLOR = RISK
  paper:    '#4f9bf5',   // PAPER / SHADOW  — cold blue
  paperDim: 'rgba(79,155,245,0.5)',
  micro:    '#e9a23e',   // VIRTUAL / MICRO — amber
  real:     '#f0564f',   // REAL SCALED     — red

  paperSoft: 'rgba(79,155,245,0.14)',
  microSoft: 'rgba(233,162,62,0.14)',
  realSoft:  'rgba(240,86,79,0.14)',

  // PnL
  up:     '#36cd86',
  down:   '#f0564f',
  upSoft: 'rgba(54,205,134,0.13)',

  // Brand / interaction (violet condor)
  brand:     '#9d7cff',
  brand2:    '#7b5cf0',
  brandSoft: 'rgba(157,124,255,0.14)',
  cyan:      '#46c8d8',

  // Semantic states
  ok:   '#36cd86',
  warn: '#e9a23e',
  bad:  '#f0564f',
  info: '#4f9bf5',
} as const;

export const font = {
  sans: '"IBM Plex Sans", system-ui, sans-serif',
  mono: '"IBM Plex Mono", ui-monospace, "SF Mono", monospace',
} as const;

export const layout = {
  railW:   '232px',
  topbarH: '56px',
} as const;

export const radius = {
  sm: '4px',
  md: '7px',
  lg: '11px',
} as const;

export const shadow = {
  sh1:  '0 1px 0 rgba(255,255,255,0.02) inset, 0 2px 8px rgba(0,0,0,0.4)',
  sh2:  '0 8px 30px rgba(0,0,0,0.55)',
  glow: '0 0 0 1px #283140, 0 0 24px rgba(157,124,255,0.12)',
} as const;
