import { describe, expect, it } from 'vitest';
import { visited } from './later.js';

const home = { name: 'home', params: {} };
const page = (gameId) => ({ name: 'game', params: { gameId } });

describe('visited', () => {
  it('brings a game back when its page is opened', () => {
    const later = new Map([['celeste', home]]);
    expect(visited(later, page('celeste')).has('celeste')).toBe(false);
  });
  it('leaves it put off on any other page', () => {
    const later = new Map([['celeste', home]]);
    expect(visited(later, page('hades'))).toBe(later);
    expect(visited(later, home)).toBe(later);
  });
  it('does not bring it straight back when put off on its own page', () => {
    const here = page('celeste');
    const later = new Map([['celeste', here]]);
    expect(visited(later, here)).toBe(later);
    // Opening the page again is a new visit.
    expect(visited(later, page('celeste')).size).toBe(0);
  });
  it('reads the game out of a longer key', () => {
    const later = new Map([['celeste\u0000Config', home]]);
    const next = visited(later, page('celeste'), (k) => k.split('\u0000')[0]);
    expect(next.size).toBe(0);
    expect(later.size).toBe(1); // not changed in place
  });
});
