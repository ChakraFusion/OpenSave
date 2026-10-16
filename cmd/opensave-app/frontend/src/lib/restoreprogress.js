// How far a restore has come, in words: the phase, percent and, once it can
// be told, roughly how long is left (daemon/restoreprogress.go).
//
// The words are the catalog's (restoreProgress.*): the caller passes its
// translate function, `$t` in a component, so the line follows the chosen
// language as it changes.

const phases = {
  rebuilding: 'restoreProgress.rebuilding',
  checking: 'restoreProgress.checking',
  writing: 'restoreProgress.writing'
};

export function restoreProgressText(p, t) {
  if (!p || !p.phase) return '';
  const params = {
    phase: t(phases[p.phase] ?? 'restoreProgress.restoring'),
    percent: p.total > 0 ? Math.floor((p.done / p.total) * 100) : 0,
    done: p.done.toLocaleString(),
    total: p.total.toLocaleString()
  };
  if (p.secondsLeft > 0) return t('restoreProgress.lineWithTimeLeft', { ...params, left: timeLeft(p.secondsLeft, t) });
  return t('restoreProgress.line', params);
}

function timeLeft(s, t) {
  if (s < 60) return t('restoreProgress.underAMinute');
  return t('restoreProgress.aboutMinutes', { n: Math.round(s / 60) });
}
