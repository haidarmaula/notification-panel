import { mkdtemp, mkdir, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { createServer } from 'vite';
import { expect, test, vi } from 'vitest';

test('local API reference serves only its manifest and read methods', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'hello-local-docs-'));
  let server: Awaited<ReturnType<typeof createServer>> | undefined;
  try {
    const docs = join(directory, 'docs');
    const tls = join(directory, 'tls');
    await mkdir(docs);
    await mkdir(tls);
    await writeFile(join(docs, 'index.html'), '<h1>Local API reference</h1>');
    await writeFile(join(docs, 'catalog.json'), '{"guides":[]}');
    await writeFile(join(docs, 'private.txt'), 'not a published asset');
    await writeFile(
      join(docs, 'manifest.json'),
      JSON.stringify({ sha256: { 'index.html': 'fixture', 'catalog.json': 'fixture' } }),
    );
    for (const name of ['ca.crt', 'client.crt', 'client.key']) {
      await writeFile(join(tls, name), 'synthetic fixture; no upstream connection');
    }
    vi.stubEnv('HELLO_LOCAL_DOCS_DIR', docs);
    vi.stubEnv('HELLO_LOCAL_TLS_DIR', tls);
    server = await createServer({
      configFile: resolve('vite.config.ts'),
      logLevel: 'silent',
      server: { host: '127.0.0.1', port: 0, strictPort: false },
    });
    await server.listen();
    const address = server.httpServer?.address();
    if (!address || typeof address === 'string') throw new Error('Missing fixture listener');
    const origin = `http://127.0.0.1:${address.port}`;
    const redirect = await fetch(`${origin}/docs`, { redirect: 'manual' });
    expect(redirect.status).toBe(308);
    expect(redirect.headers.get('location')).toBe('/docs/');
    const index = await fetch(`${origin}/docs/`);
    expect(index.status).toBe(200);
    expect(index.headers.get('cache-control')).toBe('no-store');
    expect(index.headers.get('x-content-type-options')).toBe('nosniff');
    expect(await index.text()).toContain('Local API reference');
    const catalog = await fetch(`${origin}/docs/releases/local/catalog.json`);
    expect(catalog.headers.get('content-type')).toBe('application/json');
    expect(await catalog.json()).toEqual({ guides: [] });
    const head = await fetch(`${origin}/docs/`, { method: 'HEAD' });
    expect(head.status).toBe(200);
    expect(await head.text()).toBe('');
    expect((await fetch(`${origin}/docs/`, { method: 'POST' })).status).toBe(405);
    for (const path of ['private.txt', 'manifest.json', '../private.txt', '%2e%2e/private.txt']) {
      expect((await fetch(`${origin}/docs/releases/local/${path}`)).status).toBe(404);
    }
    expect((await fetch(`${origin}/docs/releases/other/catalog.json`)).status).toBe(404);
  } finally {
    await server?.close();
    vi.unstubAllEnvs();
    await rm(directory, { recursive: true, force: true });
  }
}, 20_000);
