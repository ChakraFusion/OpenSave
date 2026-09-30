import { describe, expect, it } from 'vitest';
import { isHandheld } from './devices.js';

describe('isHandheld', () => {
  it('counts both handheld types Settings offers', () => {
    expect(isHandheld('deck')).toBe(true);
    expect(isHandheld('handheld')).toBe(true);
  });
  it('takes anything else for a computer', () => {
    for (const t of ['desktop', 'mobile', '', undefined]) expect(isHandheld(t)).toBe(false);
  });
});
