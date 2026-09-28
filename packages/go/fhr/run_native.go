//go:build !js

package fhr

import "os"

// Run is a handler binary's whole main: the subprocess protocol over the real
// process streams, exiting with its status.
func Run(h Handler, info Info) {
	os.Exit(RunCLI(h, info, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
