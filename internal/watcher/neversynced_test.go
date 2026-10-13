package watcher

import (
	"testing"

	"github.com/opensave/opensave/internal/delta"
)

// Steam rewriting remotecache.vdf is not a change of the save; a value
// recorded by an older build, which counted it, is still recognisable.
func TestContentHashLeavesOutLauncherFiles(t *testing.T) {
	save := delta.Manifest{Files: map[string]delta.FileEntry{"save.sav": {Hash: "a"}}}
	withCache := delta.Manifest{Files: map[string]delta.FileEntry{
		"save.sav":        {Hash: "a"},
		"remotecache.vdf": {Hash: "steam"},
	}}
	if ContentHash(withCache, "") != ContentHash(save, "") {
		t.Error("remotecache.vdf counts toward the content hash")
	}
	if ContentHash(save, "") != save.ContentHash() {
		t.Error("a save without such files hashes differently than before")
	}
	if contentHashWithLauncherFiles(withCache, "") != withCache.ContentHash() {
		t.Error("the hash an older build recorded cannot be reproduced")
	}
	// With rules of the game's own, both leave out what the rules exclude.
	withConfig := delta.Manifest{Files: map[string]delta.FileEntry{
		"save.sav":        {Hash: "a"},
		"config.ini":      {Hash: "c"},
		"remotecache.vdf": {Hash: "steam"},
	}}
	if ContentHash(withConfig, "config.ini") != ContentHash(save, "") {
		t.Error("an excluded file or remotecache.vdf counts toward the content hash")
	}
}
