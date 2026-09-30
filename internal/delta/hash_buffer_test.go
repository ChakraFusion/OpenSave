package delta

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Hashing a file does not allocate a whole block buffer each time.
//
// Reported in GitHub #15: a Project Zomboid save is some 238,000 small files,
// and a fresh buffer of a full block per file — 64 KB or more whatever the
// file's size — made one manifest build allocate 15 GB. The garbage collector
// kept up with none of it on a busy machine.
func TestHashFileReusesItsReadBuffer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "map_1162_1180.bin")
	if err := os.WriteFile(path, make([]byte, 300), 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := HashFile(path); err != nil { // warm up
		t.Fatal(err)
	}

	const files = 200
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for i := 0; i < files; i++ {
		if _, err := HashFile(path); err != nil {
			t.Fatal(err)
		}
	}
	runtime.ReadMemStats(&after)

	perFile := (after.TotalAlloc - before.TotalAlloc) / files
	if perFile >= defaultBlockSize/2 {
		t.Errorf("hashing a 300-byte file allocates %d bytes each time, most of a %d-byte block buffer",
			perFile, defaultBlockSize)
	}
}
