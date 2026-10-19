import { describe, it, expect } from 'vitest';
import { get } from 'svelte/store';
import { locale, t } from './i18n.js';
import { restoreProgressText } from './restoreprogress.js';

// The line in a given language, as a component would say it with `$t`.
const say = (l, p) => {
  locale.set(l);
  return restoreProgressText(p, get(t));
};

describe('restoreProgressText', () => {
  it('says nothing when no restore is under way', () => {
    expect(say('en', null)).toBe('');
  });
  it('says the phase, percent and time left', () => {
    const line = say('en', { phase: 'writing', done: 60000, total: 240000, secondsLeft: 150 });
    expect(line).toContain('Writing the files');
    expect(line).toContain('25%');
    expect(line).toContain('about 3 minutes left');
  });
  it('says one minute, and less than one, in words', () => {
    expect(say('en', { phase: 'writing', done: 1, total: 2, secondsLeft: 70 })).toContain('about a minute left');
    expect(say('en', { phase: 'writing', done: 1, total: 2, secondsLeft: 20 })).toContain('under a minute left');
  });
  it('leaves out the time while it cannot be told', () => {
    expect(say('en', { phase: 'checking', done: 10, total: 100 })).not.toContain('left');
  });
  it('calls a phase it does not know restoring', () => {
    expect(say('en', { phase: 'later', done: 0, total: 0 })).toContain('Restoring');
  });
  it('says the line in the chosen language', () => {
    const line = say('zh-Hans', { phase: 'writing', done: 60000, total: 240000, secondsLeft: 150 });
    expect(line).toContain('正在写入文件');
    expect(line).toContain('25%');
    expect(line).toContain('约 3 分钟');
    expect(line).not.toContain('Writing');
  });
});
