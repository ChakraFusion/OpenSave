package presets

import (
	"path/filepath"
	"runtime"
	"strings"

	"github.com/opensave/opensave/internal/switchtitle"
)

// InsideEmulatorSaveRoot reports whether path is, or is inside, the save
// folder of an emulator on this device: a Switch title's folder within an
// emulator's NAND, a memory card folder, RetroArch's saves. Only folders that
// exist count, so it names what is here, not what could be.
//
// One of the two things a game arriving from a peer may be tracked at by
// itself (with the folders a scan noted): see p2p ensureManifestGame.
func (sc *Scanner) InsideEmulatorSaveRoot(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	for _, p := range presetDefs {
		if p.Type != "emulator" {
			continue
		}
		for _, root := range p.resolvedPaths(sc) {
			if dirExists(root) && within(root, path) {
				return true
			}
		}
	}
	return false
}

// within reports whether p is root or inside it, by its cleaned path, without
// letting "..", or a sibling sharing root's name as a prefix, count.
func within(root, p string) bool {
	root, p = filepath.Clean(root), filepath.Clean(p)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		root, p = strings.ToLower(root), strings.ToLower(p)
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// IsSwitchSaveSlot reports whether path is one Switch title's save folder in
// a yuzu-lineage NAND here: <nand>/user/save/<account>/<profile>/<title id>,
// under a profile that already exists. Such a folder is a save by its shape,
// wherever the emulator keeps its NAND — a portable install, an EmuDeck
// folder — which a preset's standard paths would not know.
//
// The other thing a game arriving from a peer may be tracked at by itself:
// SwitchSaveFolder puts a Switch save there. See p2p ensureManifestGame.
func IsSwitchSaveSlot(path string) bool {
	title := filepath.Clean(path)
	profile := filepath.Dir(title)
	account := filepath.Dir(profile)
	save := filepath.Dir(account)
	user := filepath.Dir(save)
	nand := filepath.Dir(user)
	return switchtitle.Valid(filepath.Base(title)) &&
		hexOfLen(filepath.Base(profile), 32) &&
		hexOfLen(filepath.Base(account), 16) &&
		strings.EqualFold(filepath.Base(save), "save") &&
		strings.EqualFold(filepath.Base(user), "user") &&
		strings.EqualFold(filepath.Base(nand), "nand") &&
		dirExists(profile)
}

func hexOfLen(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}
