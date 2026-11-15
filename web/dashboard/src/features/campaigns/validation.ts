import { z } from 'zod';
const bytes = (value: string) => new TextEncoder().encode(value).length;
const id = z.string().regex(/^[1-9][0-9]*$/, 'Invalid ID.');
export const draftSchema = z.object({
  title: z
    .string()
    .refine((value) => value.trim().length > 0, 'Title is required.')
    .refine((value) => bytes(value) <= 255, 'Title must be at most 255 bytes.'),
  body: z
    .string()
    .refine((value) => value.trim().length > 0, 'Message is required.')
    .refine((value) => bytes(value) <= 4096, 'Message must be at most 4096 bytes.'),
  targets: z
    .array(
      z.discriminatedUnion('type', [
        z.object({ type: z.literal('BROADCAST') }),
        z.object({ type: z.literal('INDIVIDUAL'), id }),
        z.object({ type: z.literal('SEGMENT'), id }),
      ]),
    )
    .min(1, 'Select at least one recipient.')
    .max(100, 'Select no more than 100 recipients.')
    .superRefine((targets, ctx) => {
      const keys = targets.map((target) => `${target.type}:${'id' in target ? target.id : ''}`);
      if (new Set(keys).size !== keys.length)
        ctx.addIssue({ code: 'custom', message: 'The same recipient can only be selected once.' });
      if (targets.some((target) => target.type === 'BROADCAST') && targets.length !== 1)
        ctx.addIssue({
          code: 'custom',
          message: 'All recipients cannot be combined with other targets.',
        });
    }),
});
