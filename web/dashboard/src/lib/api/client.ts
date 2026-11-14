import { z } from 'zod';

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}
export const sessionSchema = z.object({ authenticated: z.boolean(), csrfToken: z.string().min(1) });
export type Session = z.infer<typeof sessionSchema>;
export type WriteConditions = { ifMatch?: string; ifNoneMatch?: '*' };

export async function request<T>(
  path: string,
  options: {
    method?: string;
    body?: unknown;
    csrf?: string;
    idempotencyKey?: string;
    signal?: AbortSignal;
    ifMatch?: string;
    ifNoneMatch?: '*';
    onHeaders?: (headers: Headers) => void;
  } = {},
): Promise<T> {
  if (!path.startsWith('/bff/')) throw new Error('Invalid API path');
  let response: Response;
  try {
    response = await fetch(path, {
      method: options.method ?? 'GET',
      credentials: 'same-origin',
      redirect: 'error',
      cache: 'no-store',
      signal: options.signal,
      headers: {
        ...(options.body !== undefined || (options.method && options.method !== 'GET')
          ? { 'Content-Type': 'application/json' }
          : {}),
        ...(options.csrf ? { 'X-CSRF-Token': options.csrf } : {}),
        ...(options.idempotencyKey ? { 'Idempotency-Key': options.idempotencyKey } : {}),
        ...(options.ifMatch ? { 'If-Match': options.ifMatch } : {}),
        ...(options.ifNoneMatch ? { 'If-None-Match': options.ifNoneMatch } : {}),
      },
      body: options.body !== undefined ? JSON.stringify(options.body) : undefined,
    });
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error;
    throw new ApiError(
      0,
      'Hasil permintaan belum diketahui. Periksa koneksi dan status data sebelum mencoba lagi.',
    );
  }
  options.onHeaders?.(response.headers);
  if (response.status === 204) return undefined as T;
  let raw: unknown;
  try {
    raw = JSON.parse(
      await response.text(),
      (key, value: unknown, context?: { source?: string }) => {
        if (
          (key === 'id' || key.endsWith('_id')) &&
          typeof value === 'number' &&
          !Number.isSafeInteger(value)
        ) {
          if (context?.source && /^-?[0-9]+$/.test(context.source)) return context.source;
          throw new Error('Unrepresentable identifier');
        }
        return value;
      },
    );
  } catch {
    if (response.ok) throw new ApiError(502, 'Respons layanan tidak dapat dibaca dengan aman.');
    raw = null;
  }
  if (!response.ok) {
    const error = z.object({ message: z.string() }).safeParse(raw);
    throw new ApiError(
      response.status,
      error.success ? error.data.message : 'Layanan belum dapat memproses permintaan.',
    );
  }
  return raw as T;
}
export async function data<T>(path: string, signal?: AbortSignal): Promise<T> {
  const response = await request<{ data: T }>(`/bff/api${path}`, { signal });
  return response.data;
}
export async function versionedData<T>(path: string, signal?: AbortSignal) {
  let etag: string | null = null;
  const response = await request<{ data: T }>(`/bff/api${path}`, {
    signal,
    onHeaders: (headers) => {
      etag = headers.get('ETag');
    },
  });
  if (!etag || !/^"[^"\r\n]+"$/.test(etag)) {
    throw new ApiError(502, 'Versi data tidak tersedia. Muat ulang sebelum mengubah data.');
  }
  return { data: response.data, etag };
}
export function errorMessage(error: unknown): string {
  if (error instanceof ApiError) {
    if (
      (error.status === 403 || error.status === 422) &&
      error.message === 'current password is incorrect'
    )
      return 'Kata sandi saat ini tidak sesuai.';
    if (error.status === 401) return 'Sesi berakhir. Silakan masuk kembali.';
    if (error.status === 403) return 'Anda tidak memiliki izin untuk tindakan ini.';
    if (error.status === 404) return 'Data tidak ditemukan atau akses aplikasi sudah berubah.';
    if (error.status === 503) return 'Pengiriman sedang ditahan. Draft Anda tetap tersimpan.';
    if (error.status >= 500)
      return 'Layanan sedang bermasalah. Untuk tindakan simpan atau kirim, periksa hasilnya sebelum mengulang.';
    return error.message;
  }
  return 'Terjadi masalah saat memuat data.';
}
