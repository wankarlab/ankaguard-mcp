import type { Metadata } from 'next';
import { IBM_Plex_Sans, IBM_Plex_Mono } from 'next/font/google';
import '@/design-system/globals.css';
import { NavRail } from '@/components/nav/NavRail';
import { Ticker } from '@/components/nav/Ticker';
import { KillSwitchModal } from '@/components/killswitch/KillSwitchModal';
import ExplainDrawer from '@/components/explainability/ExplainDrawer';
import { ProductGuard } from '@/components/product/ProductGuard';
import { LiveModeBadge } from '@/components/product/LiveModeBadge';
import { isProductEdition } from '@/lib/productMode';

const ibmPlexSans = IBM_Plex_Sans({
  subsets: ['latin'],
  weight: ['400', '500', '600', '700'],
  variable: '--font-ibm-sans',
  display: 'swap',
  preload: false,
});

const ibmPlexMono = IBM_Plex_Mono({
  subsets: ['latin'],
  weight: ['400', '500', '600'],
  variable: '--font-ibm-mono',
  display: 'swap',
  preload: false,
});

const productEdition = isProductEdition();
const brand = productEdition ? 'AnkaGuard MCP' : 'AnkaArena';

export const metadata: Metadata = {
  title: {
    default: brand,
    template: `%s · ${brand}`,
  },
  description: productEdition ? 'Consola del servidor MCP de trading protegido — AnkaGuard MCP' : 'Plataforma de trading IA — AnkaArena',
  robots: 'noindex,nofollow',
  icons: { icon: '/favicon.svg', shortcut: '/favicon.svg' },
};

import { NextIntlClientProvider } from 'next-intl';
import { getMessages } from 'next-intl/server';
import { notFound } from 'next/navigation';
import { routing } from '@/i18n/routing';

export default async function RootLayout({
  children,
  params
}: {
  children: React.ReactNode;
  params: Promise<{ locale: string }>;
}) {
  const { locale } = await params;

  // Ensure that the incoming `locale` is valid
  if (!routing.locales.includes(locale as any)) {
    notFound();
  }

  // Providing all messages to the client side
  const messages = await getMessages();

  return (
    <html
      lang={locale}
      className={`${ibmPlexSans.variable} ${ibmPlexMono.variable}`}
      suppressHydrationWarning
    >
      <body>
        <NextIntlClientProvider messages={messages}>
          <div className="app">
            {/* Left rail navigation */}
            <NavRail />

            {/* Main content column */}
            <main className="main">
              {/* Topbar */}
              <header className="topbar">
                <div className="tb-spacer" />
                {/* Product edition: permanent real-money indicator. */}
                {isProductEdition() && <LiveModeBadge />}
                <Ticker />
              </header>

              {/* Scrollable content */}
              <div className="content">
                {/* No-op outside the product edition; license gate inside it. */}
                <ProductGuard>{children}</ProductGuard>
              </div>
            </main>

            {/* Global explain drawer — available in all screens */}
            <ExplainDrawer />

            {/* Global kill switch — opened from the NavRail button on any screen */}
            <KillSwitchModal />
          </div>
        </NextIntlClientProvider>
      </body>
    </html>
  );
}
