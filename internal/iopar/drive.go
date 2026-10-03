// Package iopar runs many small file operations — a save of a quarter of a
// million files restored, checked, written — as many at a time as the drive
// they are on does best: one at a time on a spinning disk, whose head would
// otherwise be sent back and forth across it; many at once on an SSD, which
// works through several requests in parallel and is otherwise left waiting
// on each file's own small delays (its creation, a virus scanner's look at
// it). How many is a starting point by kind of drive, adjusted as the work
// goes by what it actually achieves (Run).
package iopar

// Kind is the kind of drive a path is on.
type Kind int

const (
	// Unknown: it could not be told. Treated as a SATA SSD — parallel, but
	// modestly so.
	Unknown Kind = iota
	HDD
	SATA
	NVMe
)

func (k Kind) String() string {
	switch k {
	case HDD:
		return "hard disk"
	case SATA:
		return "SATA SSD"
	case NVMe:
		return "NVMe SSD"
	}
	return "unknown drive"
}

// start is how many operations run at once to begin with.
func (k Kind) start() int {
	switch k {
	case HDD:
		return 1
	case NVMe:
		return 32
	}
	return 8
}

// adapts reports whether the number is adjusted as the work goes. A spinning
// disk is not: it does best one at a time, and finding that out would cost
// the seeking it exists to avoid.
func (k Kind) adapts() bool { return k != HDD }

// DriveKind says what kind of drive path is on.
func DriveKind(path string) Kind { return driveKind(path) }
