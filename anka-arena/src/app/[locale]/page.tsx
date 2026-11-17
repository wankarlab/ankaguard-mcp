import { getLocale } from 'next-intl/server';
import { redirect } from '@/i18n/routing';
import { isPlatformMode, rootPath } from '@/lib/consoleMode';

export default async function RootPage() {
  // /home in operator mode, /flywheel in platform mode.
  redirect({ href: rootPath(isPlatformMode()), locale: await getLocale() });
}
