'use client';

import useSWR from 'swr';
import { useUIStore } from '@/stores/ui';
import { getLicense, getProductStatus, type LicenseStatus, type ProductStatus } from '@/lib/product-api';

// Both requests need an operator session: without one they are not sent.
// ProductLogin revalidates every key on sign-in, which also covers a 401
// cached from an earlier session. The keys stay plain strings so
// mutate('product:license') keeps working.

/** License status; shared SWR key so every screen reuses one request. */
export function useLicense() {
  const loggedIn = useUIStore((s) => s.isLoggedIn);
  return useSWR<LicenseStatus>(loggedIn ? 'product:license' : null, getLicense, {
    revalidateOnFocus: false,
    refreshInterval: 60_000,
    shouldRetryOnError: false,
  });
}

/** Product status (live-money flag). A failing or missing endpoint yields `error`. */
export function useProductStatus() {
  const loggedIn = useUIStore((s) => s.isLoggedIn);
  return useSWR<ProductStatus>(loggedIn ? 'product:status' : null, getProductStatus, {
    revalidateOnFocus: false,
    refreshInterval: 30_000,
    shouldRetryOnError: false,
  });
}
