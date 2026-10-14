// Package huntmark is HUNT-ONLY: timestamped phase markers on stderr for the
// Windows "daemon never became ready" flake, enabled by OPENSAVE_HUNT.
package huntmark

import (
	"fmt"
	"os"
	"time"
)

var start = time.Now()
var on = os.Getenv("OPENSAVE_HUNT") != ""

// Mark writes one phase marker with the time since the process started.
func Mark(phase string) {
	if !on {
		return
	}
	fmt.Fprintf(os.Stderr, "[hunt %s +%dms pid=%d] %s\n",
		time.Now().Format("15:04:05.000"), time.Since(start).Milliseconds(), os.Getpid(), phase)
}
