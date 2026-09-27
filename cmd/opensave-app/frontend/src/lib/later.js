// A decision put off: out of the way until its game's page is opened again.

/**
 * The put-off map (key → the view it was put off from) with the entries for
 * the game `view` shows taken out — except any put off from that very view,
 * so putting one off on the game's own page does not bring it straight back.
 * Returns the same map when nothing changes. `gameOf` turns a key into its
 * game id, for callers whose keys say more than the game.
 */
export function visited(map, view, gameOf = (key) => key) {
  const gid = view?.name === 'game' ? view.params?.gameId : null;
  if (!gid) return map;
  let next = map;
  for (const [key, from] of map) {
    if (gameOf(key) === gid && from !== view) {
      if (next === map) next = new Map(map);
      next.delete(key);
    }
  }
  return next;
}
