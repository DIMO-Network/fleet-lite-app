import { defineConfig } from 'vitest/config';

// Separate from vite.config.js on purpose: the dev server's plugins (mkcert,
// eslint, static copy) have no business running under tests.
export default defineConfig({
    test: {
        include: ['src/**/*.test.ts'],
        environment: 'node',
    },
});
