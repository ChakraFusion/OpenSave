import { describe, expect, it } from 'vitest';
import { ringBox, ringFor, RING_GAP } from './focusring.js';

// A stand-in for an element: what it matches, as a set of selectors.
const el = ({ focusVisible = false, text = false } = {}) => {
  const doc = { body: null };
  return {
    nodeType: 1,
    ownerDocument: doc,
    matches(sel) {
      if (sel === ':focus-visible') return focusVisible;
      if (sel.startsWith('input')) return text;
      return false;
    }
  };
};

describe('ringFor', () => {
  it('rings what the keyboard selected', () => {
    expect(ringFor(el({ focusVisible: true }))).toBe(true);
  });

  it('does not ring what a click focused', () => {
    expect(ringFor(el({ focusVisible: false }))).toBe(false);
  });

  it('rings anything the controller selected', () => {
    expect(ringFor(el({ focusVisible: false }), { padMoving: true })).toBe(true);
    expect(ringFor(el({ text: true }), { padMoving: true })).toBe(true);
  });

  it('leaves a text field the keyboard reached to its own border', () => {
    expect(ringFor(el({ focusVisible: true, text: true }))).toBe(false);
  });

  it('rings nothing when nothing is focused', () => {
    expect(ringFor(null)).toBe(false);
    const body = { nodeType: 1, matches: () => true };
    body.ownerDocument = { body };
    expect(ringFor(body, { padMoving: true })).toBe(false);
  });
});

describe('ringBox', () => {
  it('sits around the control by the gap', () => {
    expect(ringBox({ left: 10, top: 20, width: 100, height: 40 })).toEqual({
      x: 10 - RING_GAP,
      y: 20 - RING_GAP,
      width: 100 + RING_GAP * 2,
      height: 40 + RING_GAP * 2
    });
  });
});
