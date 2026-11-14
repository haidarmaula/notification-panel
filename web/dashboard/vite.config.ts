import { defineConfig } from 'vitest/config';
import solid from 'vite-plugin-solid';
import tailwind from '@tailwindcss/vite';

const proxy = process.env.HELLO_DASHBOARD_PROXY ?? 'http://127.0.0.1:8090';
const target = new URL(proxy);
if (
  target.protocol !== 'http:' ||
  !['127.0.0.1', 'localhost'].includes(target.hostname) ||
  target.username ||
  target.password ||
  target.pathname !== '/' ||
  target.search ||
  target.hash
) {
  throw new Error('Development BFF proxy must be a loopback HTTP origin');
}

export default defineConfig({
  plugins: [solid(), tailwind()],
  server: { proxy: { '/bff': proxy } },
  build: { target: 'es2022' },
  test: { environment: 'node', include: ['src/**/*.test.ts'] },
});
