import { describe, expect, it } from 'vitest';
import { MASK_CHAR, maskText } from './textMask';

describe('maskText', () => {
  it('shows one bullet per character', () => {
    expect(maskText('hunter2')).toBe(MASK_CHAR.repeat(7));
    expect(maskText('123456')).toBe('••••••');
  });

  it('is empty for an empty value', () => {
    expect(maskText('')).toBe('');
  });

  it('counts an astral character once', () => {
    expect(maskText('a😀b')).toBe('•••');
  });

  it('keeps spaces masked too', () => {
    expect(maskText('a b')).toBe('•••');
  });
});
