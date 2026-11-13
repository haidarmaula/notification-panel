import { describe, it, expect } from 'vitest';
import { draftSchema } from './validation';
const valid = {
  title: 'Pengingat',
  body: 'Pesan',
  targets: [{ type: 'INDIVIDUAL', id: '9007199254740993' }],
};
describe('draft boundaries', () => {
  it('keeps IDs larger than JS safe integers as strings', () => {
    expect(draftSchema.parse(valid).targets[0]).toEqual(valid.targets[0]);
  });
  it('enforces UTF-8 bytes rather than character count', () => {
    expect(draftSchema.safeParse({ ...valid, title: '📨'.repeat(64) }).success).toBe(false);
    expect(draftSchema.safeParse({ ...valid, title: '📨'.repeat(63) }).success).toBe(true);
  });
  it('rejects duplicate or mixed broadcast targets', () => {
    expect(
      draftSchema.safeParse({ ...valid, targets: [...valid.targets, ...valid.targets] }).success,
    ).toBe(false);
    expect(
      draftSchema.safeParse({ ...valid, targets: [{ type: 'BROADCAST' }, ...valid.targets] })
        .success,
    ).toBe(false);
  });
  it('requires an audience and nonblank content', () => {
    expect(draftSchema.safeParse({ ...valid, targets: [] }).success).toBe(false);
    expect(draftSchema.safeParse({ ...valid, body: '   ' }).success).toBe(false);
  });
});
