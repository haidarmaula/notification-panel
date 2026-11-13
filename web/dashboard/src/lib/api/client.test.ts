import { afterEach, expect, it, vi } from 'vitest';
import { ApiError, request } from './client';
afterEach(() => vi.unstubAllGlobals());
it('uses same-origin cookies and CSRF without exposing backend credentials', async () => {
  const fetch = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
  vi.stubGlobal('fetch', fetch);
  await request('/bff/api/apps', { method: 'POST', body: {}, csrf: 'fixture-csrf' });
  expect(fetch.mock.calls[0][1]).toMatchObject({
    credentials: 'same-origin',
    redirect: 'error',
    cache: 'no-store',
    headers: { 'X-CSRF-Token': 'fixture-csrf' },
  });
  expect(fetch.mock.calls[0][1].headers).not.toHaveProperty('X-API-Key');
});
it('does not retry an uncertain mutation', async () => {
  const fetch = vi.fn().mockRejectedValue(new TypeError('connection lost'));
  vi.stubGlobal('fetch', fetch);
  await expect(
    request('/bff/api/apps', { method: 'POST', body: {}, csrf: 'test' }),
  ).rejects.toMatchObject({ status: 0 });
  expect(fetch).toHaveBeenCalledTimes(1);
});
it('preserves hold/conflict status and rejects external paths', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn().mockResolvedValue(new Response('{"message":"held"}', { status: 503 })),
  );
  await expect(request('/bff/api/apps')).rejects.toBeInstanceOf(ApiError);
  await expect(request('https://evil.invalid/')).rejects.toThrow('Invalid API path');
});
