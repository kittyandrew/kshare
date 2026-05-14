package main

import (
	"fmt"
	"os"
)

// parseTTLFlag walks args looking for `--ttl <value>` and `--help`/`-h`.
// Returns the parsed TTL (or "" if absent) and the leftover positional
// arguments. Prints `usage` and exits 0 on a help flag. Used by
// `kshare <file>` (upload) and `kshare replace <slug> <file>`; list
// has a different shape and rolls its own parsing.
func parseTTLFlag(args []string, usage string) (ttl string, pos []string) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--ttl":
			if i+1 >= len(args) {
				fail("kshare: --ttl requires a duration argument (e.g. 24h)")
			}
			ttl = args[i+1]
			i++
		case "--help", "-h":
			fmt.Fprintln(os.Stderr, "usage: "+usage)
			os.Exit(0)
		default:
			pos = append(pos, args[i])
		}
	}
	return ttl, pos
}
