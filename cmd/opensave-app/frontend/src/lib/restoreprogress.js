// How far a restore has come, in words: the phase, percent and, once it can
// be told, roughly how long is left (daemon/restoreprogress.go).

const phases = {
  rebuilding: 'Rebuilding the snapshot',
  checking: 'Checking the snapshot',
  writing: 'Writing the files'
};

export function restoreProgressText(p) {
  if (!p || !p.phase) return '';
  const what = phases[p.phase] ?? 'Restoring';
  const pct = p.total > 0 ? Math.floor((p.done / p.total) * 100) : 0;
  let text = `${what} — ${pct}% (${p.done.toLocaleString()} of ${p.total.toLocaleString()})`;
  if (p.secondsLeft > 0) text += `, ${timeLeft(p.secondsLeft)} left`;
  return text;
}

function timeLeft(s) {
  if (s < 60) return 'under a minute';
  const m = Math.round(s / 60);
  return m === 1 ? 'about a minute' : `about ${m} minutes`;
}
