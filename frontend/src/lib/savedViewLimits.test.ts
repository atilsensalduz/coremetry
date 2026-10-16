import { describe, it, expect } from 'vitest';
import { savedViewTooLarge, SAVED_VIEW_QUERY_MAX } from './savedViewLimits';

// v0.10.1236 — sunucuyla aynı tavan.
describe('savedViewTooLarge', () => {
  it('tavana eşit sığar, üstü reddedilir', () => {
    expect(savedViewTooLarge('a'.repeat(SAVED_VIEW_QUERY_MAX))).toBe('');
    expect(savedViewTooLarge('a'.repeat(SAVED_VIEW_QUERY_MAX + 1))).toMatch(/çok büyük/);
  });
});
