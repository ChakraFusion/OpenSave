package delta

import (
	"os"
	"path/filepath"
)

// maxRootLinks bounds how many links in a row RealRoot follows, so a loop of
// links ends rather than spins.
const maxRootLinks = 8

// RealRoot is the folder a save folder's files are really in, when the save
// folder itself is a link: a symbolic link, or on Windows a junction.
// Otherwise it is root, unchanged.
//
// Emulator setups make these. EmuDeck, for one, turns PCSX2's memcards folder
// into a link to ~/Emulation/saves. A walk does not enter a link, rightly for
// the links inside a save folder, which can lead anywhere (they are still
// skipped). Applied to the save folder itself, it read a folder full of
// memory cards as empty, with no error: every sync took the cards for
// deleted, and the deletion reached the other devices.
//
// So whatever walks a whole save folder walks RealRoot of it. What it finds
// is named relative to that, exactly as it would be in the folder itself.
func RealRoot(root string) string {
	p := root
	for i := 0; i < maxRootLinks; i++ {
		info, err := os.Lstat(p)
		if err != nil || info.Mode()&(os.ModeSymlink|os.ModeIrregular) == 0 {
			return p
		}
		target, err := os.Readlink(p)
		if err != nil || target == "" {
			return p
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(p), target)
		}
		next := filepath.Clean(target)
		if st, err := os.Stat(next); err != nil || !st.IsDir() {
			return p // a link to a file, or to nothing: left as it is
		}
		p = next
	}
	return p
}
