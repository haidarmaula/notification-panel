import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import { themeFontAliases } from './theme-fonts.config.ts';
import { readFileSync } from 'node:fs';
import { Agent } from 'node:https';
import { join } from 'node:path';

const docsDirectory = process.env.HELLO_LOCAL_DOCS_DIR;
const tlsDirectory = process.env.HELLO_LOCAL_TLS_DIR;
const docsUpstream = new URL(process.env.HELLO_LOCAL_API_UPSTREAM ?? 'https://127.0.0.1:18082');
if (
  docsDirectory && tlsDirectory &&
  (docsUpstream.protocol !== 'https:' || docsUpstream.hostname !== '127.0.0.1' ||
    docsUpstream.username || docsUpstream.password || docsUpstream.pathname !== '/' ||
    docsUpstream.search || docsUpstream.hash)
) {
  throw new Error('Development API reference proxy must be a loopback HTTPS origin');
}
const localDocs =
  docsDirectory && tlsDirectory
    ? {
        name: 'hello-local-api-docs',
        configureServer(server: import('vite').ViteDevServer) {
          const manifest = JSON.parse(readFileSync(join(docsDirectory, 'manifest.json'), 'utf8'));
          const types: Record<string, string> = {
            html: 'text/html',
            js: 'text/javascript',
            css: 'text/css',
            json: 'application/json',
            png: 'image/png',
            svg: 'image/svg+xml',
          };
          server.middlewares.use((request, response, next) => {
            const path = new URL(request.url ?? '/', 'http://localhost').pathname;
            if (path !== '/docs' && !path.startsWith('/docs/')) return next();
            if (path === '/docs') {
              response.writeHead(308, { Location: '/docs/' });
              return response.end();
            }
            if (request.method !== 'GET' && request.method !== 'HEAD') {
              response.writeHead(405, { Allow: 'GET, HEAD' });
              return response.end();
            }
            const file =
              path === '/docs/'
                ? 'index.html'
                : path.startsWith('/docs/releases/local/')
                  ? path.slice('/docs/releases/local/'.length)
                  : '';
            if (!Object.hasOwn(manifest.sha256, file)) {
              response.writeHead(404);
              return response.end();
            }
            response.setHeader(
              'Content-Type',
              types[file.split('.').pop() ?? ''] ?? 'application/octet-stream',
            );
            response.setHeader('Cache-Control', 'no-store');
            response.setHeader('X-Content-Type-Options', 'nosniff');
            response.setHeader('Referrer-Policy', 'no-referrer');
            response.end(
              request.method === 'HEAD' ? undefined : readFileSync(join(docsDirectory, file)),
            );
          });
        },
      }
    : undefined;

const proxy = process.env.HELLO_DASHBOARD_PROXY ?? 'http://127.0.0.1:8090';
const localControl = process.env.HELLO_LOCAL_FCM_CONTROL;
const localDashboard = process.env.HELLO_LOCAL_DASHBOARD_ORIGIN;
const localDevices = localControl === 'http://127.0.0.1:18191' && localDashboard === 'http://localhost:5273';
const canonicalLocalDashboard = localDevices ? {
  name: 'hello-real-fcm-origin',
  configureServer(server: import('vite').ViteDevServer) {
    server.middlewares.use((request, response, next) => {
      if (request.headers.host !== '127.0.0.1:5273' ||
          (request.method !== 'GET' && request.method !== 'HEAD') ||
          !request.headers.accept?.includes('text/html')) return next();
      const path = request.url || '/';
      if (!path.startsWith('/') || path.startsWith('//') || path.includes('\\')) return next();
      response.writeHead(302, { Location: `${localDashboard}${path}`, 'Cache-Control': 'no-store' });
      response.end();
    });
  },
} : undefined;
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
  plugins: [react(), localDocs, canonicalLocalDashboard],
  resolve: { alias: themeFontAliases },
  server: {
    proxy: {
      '/bff': proxy,
      ...(localDevices ? { '/__hello_dev/': { target: localControl, changeOrigin: false } } : {}),
      ...(localDocs && tlsDirectory
        ? {
            '/api/v1/': {
              target: docsUpstream.origin,
              agent: new Agent({
                ca: readFileSync(join(tlsDirectory, 'ca.crt')),
                cert: readFileSync(join(tlsDirectory, 'client.crt')),
                key: readFileSync(join(tlsDirectory, 'client.key')),
              }),
            },
          }
        : {}),
    },
  },
  build: { target: 'es2022' },
  test: { environment: 'node', include: ['src/**/*.test.ts'] },
});
