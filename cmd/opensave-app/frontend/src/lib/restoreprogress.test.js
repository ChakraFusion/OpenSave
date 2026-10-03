import { describe, it, expect } from 'vitest';
import { restoreProgressText } from './restoreprogress.js';

describe('restoreProgressText', () => {
  it('says nothing when no restore is under way', () => {
    expect(restoreProgressText(null)).toBe('');
  });
  it('says the phase, percent and time left', () => {
    const t = restoreProgressText({ phase: 'writing', done: 60000, total: 240000, secondsLeft: 150 });
    expect(t).toContain('Writing the files');
    expect(t).toContain('25%');
    expect(t).toContain('about 3 minutes left');
  });
  it('leaves out the time while it cannot be told', () => {
    expect(restoreProgressText({ phase: 'checking', done: 10, total: 100 })).not.toContain('left');
  });
});
