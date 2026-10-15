// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

/// <reference types="vitest" />
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: './vitest.setup.ts',
    watch: false,
    include: ['{components,hooks,lib}/**/__tests__/**/*.test.{ts,tsx}'],
    silent: true,
    // Dialog and player tests take up to 1.4 s each when idle and up to about
    // 8 s when every core is busy, mostly rendering and querying in jsdom, so
    // the 5 s default fails them under load.
    testTimeout: 20000,
  },
  resolve: {
    alias: {
      '@/': new URL('./', import.meta.url).pathname,
    },
  },
});
