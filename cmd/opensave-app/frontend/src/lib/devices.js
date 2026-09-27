// What kind of device another one is, from the type it reports.

/** A Steam Deck, or a handheld PC set as one in Settings (an Ally, a Legion
 *  Go). Everything else is shown as a computer. */
export const isHandheld = (type) => type === 'deck' || type === 'handheld';
