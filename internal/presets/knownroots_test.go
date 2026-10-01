package presets

import (
	"os"
	"path/filepath"
	"testing"
)

// A Switch title's save slot is a save folder by its shape, wherever the NAND
// is; nothing else that merely ends in a title id is.
func TestIsSwitchSaveSlot(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, "Eden Portable", "user", "nand", "user", "save", "0000000000000000", "0123456789ABCDEF0123456789ABCDEF")
	if err := os.MkdirAll(profile, 0o777); err != nil {
		t.Fatal(err)
	}
	slot := filepath.Join(profile, "0100ABCDEF012000")
	if !IsSwitchSaveSlot(slot) {
		t.Errorf("a title's slot under an existing profile was not recognised: %s", slot)
	}

	notSlots := map[string]string{
		"no such profile here": filepath.Join(root, "nand", "user", "save", "0000000000000000", "FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF", "0100ABCDEF012000"),
		"not a title id":       filepath.Join(profile, "id_ed25519"),
		"a folder named save":  filepath.Join(root, "save", "x", "y", "0100ABCDEF012000"),
		"a home folder":        filepath.Join(root, ".ssh"),
	}
	for why, p := range notSlots {
		if IsSwitchSaveSlot(p) {
			t.Errorf("%s: %s was taken for a Switch save slot", why, p)
		}
	}
}

func TestInsideEmulatorSaveRootNeedsAnEmulatorHere(t *testing.T) {
	phone := t.TempDir()
	sc := &Scanner{GOOS: "linux", HomeDir: phone}
	if err := os.MkdirAll(filepath.Join(phone, ".config", "retroarch", "saves"), 0o777); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	if !sc.InsideEmulatorSaveRoot(filepath.Join(phone, ".config", "retroarch", "saves", "Pokemon.srm")) {
		t.Error("a file in RetroArch's saves folder was not inside an emulator's save root")
	}
	for _, p := range []string{
		filepath.Join(phone, ".config", "retroarch"),             // the emulator's own settings, above its saves
		filepath.Join(phone, ".config", "retroarch-evil"),        // a sibling sharing the name
		filepath.Join(phone, ".config", "retroarch", "..", "gh"), // climbing out
		filepath.Join(phone, ".ssh"),
	} {
		if sc.InsideEmulatorSaveRoot(p) {
			t.Errorf("%s was taken for an emulator save folder", p)
		}
	}
}
