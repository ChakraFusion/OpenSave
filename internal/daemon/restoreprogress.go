package daemon

import (
	"sync"
	"time"
)

// How far a restore has come, for the window: a large save is put back in
// minutes, and a spinner alone left it unclear whether anything was
// happening at all.

// RestoreProgress is a restore under way.
type RestoreProgress struct {
	Phase string `json:"phase"` // snapshot.PhaseRebuilding, PhaseChecking, PhaseWriting
	Done  int    `json:"done"`
	Total int    `json:"total"`
	// SecondsLeft estimates how long the phase still takes; 0 until there
	// is enough to go on.
	SecondsLeft int `json:"secondsLeft,omitempty"`
}

type restoreState struct {
	mu    sync.Mutex
	games map[string]*restoreEntry
}

type restoreEntry struct {
	RestoreProgress
	phaseAt time.Time // when the phase began
	sentAt  time.Time // when the window was last told
}

// noteRestoreProgress records a restore's progress (snapshot.Manager's
// OnRestoreProgress), telling the window about once a second.
func (d *Daemon) noteRestoreProgress(gameID, phase string, done, total int) {
	now := time.Now()
	d.restoring.mu.Lock()
	if d.restoring.games == nil {
		d.restoring.games = map[string]*restoreEntry{}
	}
	tell := false
	if phase == "" {
		delete(d.restoring.games, gameID)
		tell = true
	} else {
		e := d.restoring.games[gameID]
		if e == nil || e.Phase != phase {
			e = &restoreEntry{phaseAt: now}
			d.restoring.games[gameID] = e
			tell = true
		}
		e.Phase, e.Done, e.Total = phase, done, total
		e.SecondsLeft = 0
		if elapsed := now.Sub(e.phaseAt); done > 0 && total > done && elapsed > 2*time.Second {
			e.SecondsLeft = int(elapsed.Seconds() / float64(done) * float64(total-done))
		}
		if now.Sub(e.sentAt) >= time.Second {
			tell = true
		}
		if tell {
			e.sentAt = now
		}
	}
	d.restoring.mu.Unlock()
	if tell && d.OnGameChanged != nil {
		d.OnGameChanged(gameID)
	}
}

// RestoringNow is the progress of a restore of gameID under way, or nil.
func (d *Daemon) RestoringNow(gameID string) *RestoreProgress {
	d.restoring.mu.Lock()
	defer d.restoring.mu.Unlock()
	if e := d.restoring.games[gameID]; e != nil {
		p := e.RestoreProgress
		return &p
	}
	return nil
}
