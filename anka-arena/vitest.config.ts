// Minimal Vitest config for pure-function smoke tests (no DOM, no Next runtime).
// Resolves the `@/*` path alias to mirror tsconfig.json so adapter/api modules
// import cleanly. Intentionally lightweight: no jsdom, no @testing-library — the
// highest-value, lowest-risk target is the envelope unwrap + snake_case→camelCase
// adapters, which are pure functions.
import { defineConfig } from 'vitest/config';
import { fileURLToPath } from 'node:url';

export default defineConfig({
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  // tsconfig uses jsx:'preserve' (Next); vitest needs the automatic runtime for .tsx tests.
  esbuild: { jsx: 'automatic' },
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
  },
});
