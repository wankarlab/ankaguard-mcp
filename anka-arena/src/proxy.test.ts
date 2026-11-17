import { afterEach, describe, expect, it, vi } from 'vitest';
import { NextRequest } from 'next/server.js';

// next-intl/middleware no se resuelve como ESM bajo vitest; aquí solo importa
// que lo alcance (o no) la petición, así que se sustituye por un marcador.
vi.mock('next-intl/middleware', () => ({
  default: () => () => new Response(null, { headers: { 'x-intl': '1' } }),
}));

vi.mock('./i18n/routing', () => ({ routing: { locales: ['es', 'en'], defaultLocale: 'es' } }));

const { default: proxy } = await import('./proxy');

// Escenario "Ruta ajena al producto": con NEXT_PUBLIC_EDITION=product una
// pantalla que no pertenece al producto se reescribe a una ruta sin página
// (404 de Next); las del producto pasan por next-intl.
describe('proxy en la edición producto', () => {
  afterEach(() => vi.unstubAllEnvs());

  const rewriteTarget = (res: Response) => res.headers.get('x-middleware-rewrite') ?? '';

  it('reescribe a /_edition-not-found una ruta ajena al producto', () => {
    vi.stubEnv('NEXT_PUBLIC_EDITION', 'product');
    for (const p of ['/flywheel', '/xsec', '/en/arena/3']) {
      const res = proxy(new NextRequest(`http://localhost:3002${p}`));
      expect(rewriteTarget(res), p).toContain('/_edition-not-found');
    }
  });

  it('no reescribe una pantalla del producto', () => {
    vi.stubEnv('NEXT_PUBLIC_EDITION', 'product');
    for (const p of ['/license', '/agent-tokens', '/en/system-health']) {
      const res = proxy(new NextRequest(`http://localhost:3002${p}`));
      expect(rewriteTarget(res), p).not.toContain('/_edition-not-found');
      expect(res.headers.get('x-intl'), p).toBe('1');
    }
  });

  it('fuera de la edición producto no hay "no encontrada" por edición', () => {
    vi.stubEnv('NEXT_PUBLIC_EDITION', '');
    const res = proxy(new NextRequest('http://localhost:3002/flywheel'));
    expect(rewriteTarget(res)).not.toContain('/_edition-not-found');
  });
});
