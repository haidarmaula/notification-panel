import { afterEach, expect, it, vi } from 'vitest';
import { ApiError, request, versionedData } from './client';
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
it('preserves int64 identifiers without rounding and leaves ordinary numbers numeric', async () => {
  vi.stubGlobal(
    'fetch',
    vi
      .fn()
      .mockResolvedValue(
        new Response('{"data":{"id":9223372036854775807,"staff_id":9007199254740993,"total":20}}'),
      ),
  );
  await expect(request('/bff/api/staff/9223372036854775807')).resolves.toEqual({
    data: { id: '9223372036854775807', staff_id: '9007199254740993', total: 20 },
  });
});
it('passes conditional writes without retrying a stale version', async () => {
  const fetch = vi
    .fn()
    .mockResolvedValue(new Response('{"message":"precondition failed"}', { status: 412 }));
  vi.stubGlobal('fetch', fetch);
  await expect(
    request('/bff/api/admin/apps/example', {
      method: 'PATCH',
      body: { name: 'Renamed' },
      csrf: 'csrf',
      ifMatch: '"app:example:1"',
    }),
  ).rejects.toMatchObject({ status: 412 });
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(fetch.mock.calls[0][1].headers).toMatchObject({
    'If-Match': '"app:example:1"',
    'X-CSRF-Token': 'csrf',
  });
});
it('retains actual strong resource versions and rejects missing or weak versions', async () => {
  const fetch = vi
    .fn()
    .mockResolvedValueOnce(
      new Response('{"data":{"name":"Current"}}', { headers: { ETag: '"app:example:2"' } }),
    )
    .mockResolvedValueOnce(new Response('{"data":{}}'))
    .mockResolvedValueOnce(new Response('{"data":{}}', { headers: { ETag: 'W/"app:example:2"' } }));
  vi.stubGlobal('fetch', fetch);
  await expect(versionedData('/admin/apps/example')).resolves.toEqual({
    data: { name: 'Current' },
    etag: '"app:example:2"',
  });
  await expect(versionedData('/admin/apps/example')).rejects.toMatchObject({ status: 502 });
  await expect(versionedData('/admin/apps/example')).rejects.toMatchObject({ status: 502 });
});
