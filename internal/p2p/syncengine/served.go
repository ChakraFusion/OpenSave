package syncengine

import (
	"time"

	"github.com/opensave/opensave/internal/delta"
)

// What this device hands a peer is a state both have held.
//
// A merge base is a state both devices verifiably held, and the engine already
// banks one when it can prove it: a pure pull ratchets the base on the side
// that pulled, and a push is recorded (the pushed hash) so the pushing side
// can bank it once it sees the peer holding exactly what it handed over.
//
// Serving a manifest is handing over too, and it was never recorded. The peer
// that pulls from it banks the state as its base; this side did not, and kept
// judging the peer against the older agreement. Usually harmless — the next
// sync finds the two identical and agrees. Not when the state served was one
// this device held only for a moment: a game part-way through writing its
// save when the peer asked. The peer pulls that half-written state and holds
// it; the game finishes; and this side sees the peer changed since the last
// agreement and itself changed too, and raises a conflict over a save only one
// device ever touched. Reproduced by pulling half an update and finishing it.
//
// So what is served is remembered, per game and peer, and a peer found holding
// exactly one of those states is banked as agreeing on it — the same proof the
// push record gives, from the other direction.
//
// Only while nothing has been agreed since. Each record carries the base it was
// served under, and counts only if that is still the base: a record from
// before a later agreement names a state older than it, and banking it would
// move the base backwards — which can turn a real two-sided change into a
// one-sided one and overwrite it. The same reason the push record is cleared
// whenever any agreement is recorded.

type servedState struct {
	hash string // the manifest hash as served
	base string // the agreed base with that peer at the time
	at   time.Time
}

// servedKeep is how many served states are remembered per game and peer: a
// sync that pulls in several passes, or a peer that asks while this device's
// own sync is running, is several in quick succession.
const servedKeep = 16

// servedMaxAge bounds how long a served state is remembered. Long enough to
// cover a slow pull and the sync after it, short enough that nothing from
// another session lingers.
const servedMaxAge = time.Hour

func servedKey(gameID, peerID string) string { return gameID + "\x00" + peerID }

// NoteServed records that this device served the game's manifest m to peerID.
func (e *Engine) NoteServed(gameID, peerID string, m delta.Manifest) {
	if peerID == "" {
		return
	}
	st := servedState{hash: m.ManifestHash(), base: e.Store.GetAgreedHash(gameID, peerID), at: time.Now()}
	e.servedMu.Lock()
	defer e.servedMu.Unlock()
	if e.served == nil {
		e.served = map[string][]servedState{}
	}
	key := servedKey(gameID, peerID)
	list := append(e.served[key], st)
	if len(list) > servedKeep {
		list = list[len(list)-servedKeep:]
	}
	e.served[key] = list
}

// servedUnderBase reports whether this device served hash to the peer while
// base was the agreed state — and so whether a peer holding hash holds a state
// this device held since that agreement.
func (e *Engine) servedUnderBase(gameID, peerID, hash, base string) bool {
	e.servedMu.Lock()
	defer e.servedMu.Unlock()
	for _, st := range e.served[servedKey(gameID, peerID)] {
		if st.hash == hash && st.base == base && time.Since(st.at) < servedMaxAge {
			return true
		}
	}
	return false
}
