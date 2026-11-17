import createMiddleware from 'next-intl/middleware';
import { NextResponse, type NextRequest } from 'next/server';
import { routing } from './i18n/routing';
import { isPlatformMode, resolveRedirect, splitLocale } from './lib/consoleMode';
import { isProductEdition, isRouteAllowed } from './lib/productMode';

const intlMiddleware = createMiddleware(routing);

export default function proxy(request: NextRequest) {
  const { pathname } = request.nextUrl;
  const { locale, path } = splitLocale(pathname, routing.locales);

  // Single point of edition routing: a route outside the active edition answers
  // "not found" (the rewrite target matches no page, so Next renders its 404).
  const product = isProductEdition();
  if (!isRouteAllowed(path, product)) {
    const url = request.nextUrl.clone();
    url.pathname = '/_edition-not-found';
    return NextResponse.rewrite(url);
  }
  if (product) return intlMiddleware(request);

  const target = resolveRedirect(path, isPlatformMode());

  if (target) {
    // Keep the locale prefix the request came with (as-needed: none for default).
    const [targetPath, targetQuery] = target.split('?');
    const url = request.nextUrl.clone();
    url.pathname = locale ? `/${locale}${targetPath}` : targetPath;
    // Target query wins over incoming params with the same key.
    if (targetQuery) {
      new URLSearchParams(targetQuery).forEach((v, k) => url.searchParams.set(k, v));
    }
    return NextResponse.redirect(url, 307);
  }

  return intlMiddleware(request);
}

export const config = {
  // Skip all paths that should not be internationalized. This skips the folders "api", "_next" and all files with an extension (e.g. favicon.ico)
  matcher: ['/((?!api|_next|_vercel|.*\\..*).*)']
};
