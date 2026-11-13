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

export async function request<T>(
  path: string,
  options: {
    method?: string;
    body?: unknown;
    csrf?: string;
    idempotencyKey?: string;
    signal?: AbortSignal;
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
        ...(options.body !== undefined ? { 'Content-Type': 'application/json' } : {}),
        ...(options.csrf ? { 'X-CSRF-Token': options.csrf } : {}),
        ...(options.idempotencyKey ? { 'Idempotency-Key': options.idempotencyKey } : {}),
      },
      body: options.body !== undefined ? JSON.stringify(options.body) : undefined,
    });
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error;
    throw new ApiError(
      0,
      'Hasil permintaan belum diketahui. Periksa koneksi dan status campaign sebelum mencoba lagi.',
    );
  }
  if (response.status === 204) return undefined as T;
  const raw: unknown = await response.json().catch(() => null);
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
export function errorMessage(error: unknown): string {
  if (error instanceof ApiError) {
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
