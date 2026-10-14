// Command opensave-cli is the OpenSave command-line interface.
package main

import (
	"os"

	"github.com/opensave/opensave/internal/cliapp"
)

func main() {
	if len(os.Args) > 2 && os.Args[1] == "daemon" {
		cliapp.HuntMark("main: process up")
	}
	os.Exit(cliapp.Run(os.Args[1:]))
}
