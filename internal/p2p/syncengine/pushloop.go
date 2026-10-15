package syncengine

import (
	"time"
)

// Two devices pushing to each other forever (#71).
//
// The agreed base decides which side moved (#42): a side whose whole manifest
// is the base has not changed. That is right except when the same bytes come
// back after the base was agreed. One device's base can still be a round
// behind — the report of the other's pull not yet processed — and a save
// written back to exactly that older state then reads as "unchanged" there.
// That device pushes, the other correctly sees itself as newer and pushes too,
// neither pulls, nothing moves either base, and they ask each other to pull
// several times a second. The change never arrives.
//
// Hashes cannot tell an unchanged save from the same bytes written again, and
// clocks cannot either (#42 exists because a restored copy of the base has a
// newer mtime). So the rule stays and the loop is caught instead: a peer that
// was just told to pull and answered by asking this device to pull, with
// neither side's content changed since, has decided the opposite of this
// device about the same two states. That is two devices disagreeing about
// which save is newer, which is what a conflict is. It is raised as one: both
// versions are kept and the user is asked, rather than either side guessing.
//
// One answered push is not yet a loop: a peer can decide to push while this
// device's trigger is still on its way. pushLoopRounds of them, each answered
// with the same two states, is.

// pushBack is the push this device last decided for a game and peer: the two
// states it was decided on, and how many times in a row the peer has answered
// it by asking this device to pull, without either state changing.
type pushBack struct {
	local, remote string
	rounds        int
}

// pushLoopRounds is how many answered pushes in a row make a loop. The loop
// runs several times a second, so stopping at two costs nothing, and one
// would mistake a peer's crossing trigger for a refusal.
const pushLoopRounds = 2

// pushLoopWindow bounds how long ago the peer's request to pull may have
// arrived to count as the answer to this device's push.
const pushLoopWindow = 2 * time.Minute

func loopKey(gameID, peerID string) string { return gameID + "\x00" + peerID }

// NoteTriggered records that peerID asked this device to pull gameID.
func (e *Engine) NoteTriggered(gameID, peerID string) {
	if peerID == "" {
		return
	}
	e.loopMu.Lock()
	defer e.loopMu.Unlock()
	if e.triggered == nil {
		e.triggered = map[string]time.Time{}
	}
	e.triggered[loopKey(gameID, peerID)] = time.Now()
}

// pushLoop is told the decision of a sync with a peer, and reports whether the
// two devices are pushing to each other without either changing. Anything but
// a push — a pull, a deletion, agreement — ends the watch, and so does either
// side's content changing.
func (e *Engine) pushLoop(gameID, peerID string, d Decision, localHash, remoteHash string) bool {
	e.loopMu.Lock()
	defer e.loopMu.Unlock()
	key := loopKey(gameID, peerID)
	// The peer's request is used up by this sync, whatever it decides: the
	// next one counts only if the peer asks again.
	at, asked := e.triggered[key]
	delete(e.triggered, key)
	asked = asked && time.Since(at) < pushLoopWindow

	pushOnly := d.HasPush() && !d.HasPull() && !d.HasDeletions()
	if !pushOnly {
		delete(e.pushBacks, key)
		return false
	}
	if e.pushBacks == nil {
		e.pushBacks = map[string]pushBack{}
	}
	prev, seen := e.pushBacks[key]
	if !seen || prev.local != localHash || prev.remote != remoteHash {
		e.pushBacks[key] = pushBack{local: localHash, remote: remoteHash}
		return false
	}
	if !asked {
		// The same push again, but not as the peer's answer: a retry of this
		// device's own, after a pull on the peer that did not happen yet.
		return false
	}
	prev.rounds++
	e.pushBacks[key] = prev
	if prev.rounds >= pushLoopRounds {
		delete(e.pushBacks, key)
		return true
	}
	return false
}
