// The highlight on what is selected, when it is selected from the keyboard or
// a controller.
//
// It used to be an outline on the control itself: thin, and cut off wherever
// the control sat in a list or a card that clips what spills over its edge —
// so moving with the D-pad or the arrow keys, it was often not plain where
// you were. Now one ring is drawn over everything, on the control's box: it
// cannot be clipped, it is the same on every kind of control, and it slides
// from one to the next, so a move is seen as a move.
//
// Shown only when focus was moved without a pointer: by the keyboard, which
// the browser reports as :focus-visible, or by the controller (data-input,
// lib/controller.js). Not after a click, and not on a text field the pointer
// put focus in, which draws its own.

/** Room between the control and the ring, in pixels. */
export const RING_GAP = 3;

/** Whether the ring is drawn on el, focused, given how focus got there. */
export function ringFor(el, { padMoving = false } = {}) {
  if (!el || el.nodeType !== 1 || el === el.ownerDocument?.body) return false;
  if (padMoving) return true;
  try {
    if (!el.matches(':focus-visible')) return false;
  } catch {
    return false; // a browser without :focus-visible: no ring rather than one on every click
  }
  // A text field shows the browser's caret and its own focused border.
  return !el.matches('input:not([type="checkbox"]):not([type="radio"]):not([type="range"]), textarea, [contenteditable="true"]');
}

/** The ring's box for a control's box: around it, by RING_GAP. */
export function ringBox(rect, gap = RING_GAP) {
  return { x: rect.left - gap, y: rect.top - gap, width: rect.width + gap * 2, height: rect.height + gap * 2 };
}

/** Starts the ring. Returns a function that removes it. */
export function startFocusRing({ win = globalThis.window, doc = globalThis.document } = {}) {
  if (!win || !doc?.body) return () => {};
  const root = doc.documentElement;
  const ring = doc.createElement('div');
  ring.className = 'focus-ring';
  ring.setAttribute('aria-hidden', 'true');
  doc.body.appendChild(ring);
  // Drawn by the ring now: app.css takes the outlines off while this is set.
  root.dataset.focusRing = 'on';

  let target = null;
  let radius = 0; // the target's corner, read when it becomes the target
  let frame = 0;
  let placed = false;

  const padMoving = () => root.dataset.controller === 'on' && root.dataset.input === 'pad';

  function hide() {
    target = null;
    placed = false;
    ring.classList.remove('on');
    if (frame) win.cancelAnimationFrame(frame);
    frame = 0;
  }

  // Followed every frame while it is shown: lists scroll, cards lift, pages
  // slide in, and a ring placed once would be left behind by any of them.
  function follow() {
    frame = 0;
    if (!target || !target.isConnected || doc.activeElement !== target) {
      hide();
      return;
    }
    const r = target.getBoundingClientRect();
    if (r.width === 0 && r.height === 0) {
      hide();
      return;
    }
    const b = ringBox(r);
    // The first time, it appears in place; after that it slides there.
    if (!placed) ring.classList.add('jump');
    ring.style.transform = `translate(${b.x}px, ${b.y}px)`;
    ring.style.width = `${b.width}px`;
    ring.style.height = `${b.height}px`;
    ring.style.borderRadius = `${radius + RING_GAP}px`;
    if (!placed) {
      void ring.offsetWidth; // the jump lands before the slide is let back on
      ring.classList.remove('jump');
      placed = true;
    }
    ring.classList.add('on');
    frame = win.requestAnimationFrame(follow);
  }

  function update() {
    const el = doc.activeElement;
    if (!ringFor(el, { padMoving: padMoving() })) {
      hide();
      return;
    }
    if (el !== target) {
      target = el;
      radius = parseFloat(win.getComputedStyle(el).borderTopLeftRadius) || 0;
    }
    if (!frame) frame = win.requestAnimationFrame(follow);
  }

  // After the browser has decided :focus-visible for the new focus.
  const onFocus = () => win.requestAnimationFrame(update);
  const onPointer = () => hide();
  const onKey = (e) => {
    // Tab and the arrow keys move focus; the ring follows on focusin. A key
    // that does not move it, on a control a click focused, shows the ring
    // there now, as the browser does for :focus-visible.
    if (e.key === 'Tab' || e.key.startsWith('Arrow')) win.requestAnimationFrame(update);
  };
  doc.addEventListener('focusin', onFocus);
  doc.addEventListener('focusout', onFocus);
  win.addEventListener('pointerdown', onPointer, true);
  win.addEventListener('keydown', onKey, true);
  // The controller moving focus sets data-input before focusing: noticed
  // through focusin. Switching to the pad with focus already somewhere shows
  // the ring there too.
  const watch = new win.MutationObserver(() => update());
  watch.observe(root, { attributes: true, attributeFilter: ['data-input', 'data-controller'] });

  return () => {
    hide();
    watch.disconnect();
    doc.removeEventListener('focusin', onFocus);
    doc.removeEventListener('focusout', onFocus);
    win.removeEventListener('pointerdown', onPointer, true);
    win.removeEventListener('keydown', onKey, true);
    ring.remove();
    delete root.dataset.focusRing;
  };
}
