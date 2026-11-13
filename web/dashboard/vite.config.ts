import { defineConfig } from 'vitest/config';
import solid from 'vite-plugin-solid';
import tailwind from '@tailwindcss/vite';

export default defineConfig({
  plugins: [solid(), tailwind()],
  server: { proxy: { '/bff': 'http://127.0.0.1:8090' } },
  build: { target: 'es2022' },
  test: { environment: 'node', include: ['src/**/*.test.ts'] },
});
