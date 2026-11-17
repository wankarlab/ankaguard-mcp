export type RingId = 0 | 1 | 2;

export interface Ring {
  id: RingId;
  name: string;
  shortName: string;
  desc: string;
  scope: string;
  /** SVG radius */
  r: number;
  color: string;
  cssVar: string;
  friction: 'click' | 'bio' | 'bio+type';
  endpoint: string;
}

export type Phase = 'idle' | 'preview' | 'confirm1' | 'bio' | 'type' | 'done' | 'error';

export const RINGS: Ring[] = [
  {
    id: 1,
    name: 'ANILLO 1 · SPEC',
    shortName: 'SPEC',
    desc: 'Congela una estrategia',
    scope: 'spec seleccionado',
    r: 60,
    color: '#e9a23e',
    cssVar: 'var(--micro)',
    friction: 'click',
    endpoint: '/api/v1/admin/killswitch/ring/1',
  },
  {
    id: 2,
    name: 'ANILLO 2 · TENANT',
    shortName: 'TENANT',
    desc: 'Aísla un usuario',
    scope: 'tenant seleccionado',
    r: 105,
    color: '#4f9bf5',
    cssVar: 'var(--paper)',
    friction: 'bio',
    endpoint: '/api/v1/admin/killswitch/ring/2',
  },
  {
    id: 0,
    name: 'ANILLO 0 · GLOBAL',
    shortName: 'GLOBAL',
    desc: 'Cierra TODO · todos los tenants',
    scope: 'engine completo',
    r: 152,
    color: '#f0564f',
    cssVar: 'var(--real)',
    friction: 'bio+type',
    endpoint: '/api/v1/admin/killswitch/ring/0',
  },
];
