package presets

import (
	"path/filepath"
	"runtime"
	"strings"
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
