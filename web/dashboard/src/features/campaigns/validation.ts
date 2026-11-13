import { z } from 'zod';
const bytes = (value: string) => new TextEncoder().encode(value).length;
const id = z.string().regex(/^[1-9][0-9]*$/, 'ID tidak valid.');
export const draftSchema = z.object({
  title: z
    .string()
    .refine((value) => value.trim().length > 0, 'Judul wajib diisi.')
    .refine((value) => bytes(value) <= 255, 'Judul maksimal 255 byte.'),
  body: z
    .string()
    .refine((value) => value.trim().length > 0, 'Pesan wajib diisi.')
    .refine((value) => bytes(value) <= 4096, 'Pesan maksimal 4096 byte.'),
  targets: z
    .array(
      z.discriminatedUnion('type', [
        z.object({ type: z.literal('BROADCAST') }),
        z.object({ type: z.literal('INDIVIDUAL'), id }),
        z.object({ type: z.literal('SEGMENT'), id }),
      ]),
    )
    .min(1, 'Pilih sedikitnya satu penerima.')
    .max(100, 'Maksimal 100 target.')
    .superRefine((targets, ctx) => {
      const keys = targets.map((target) => `${target.type}:${'id' in target ? target.id : ''}`);
      if (new Set(keys).size !== keys.length)
        ctx.addIssue({ code: 'custom', message: 'Target yang sama hanya boleh dipilih sekali.' });
      if (targets.some((target) => target.type === 'BROADCAST') && targets.length !== 1)
        ctx.addIssue({
          code: 'custom',
          message: 'Semua penerima tidak dapat digabungkan dengan target lain.',
        });
    }),
});
