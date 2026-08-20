package main

import (
	"fmt"
	"os"
	"strings"
)

// parseTTLFlag walks args looking for `--ttl <value>` and `--help`/`-h`. Returns the parsed TTL ("" if
// absent) and the leftover positional arguments, printing `usage` and exiting 0 on a help flag.
func parseTTLFlag(args []string, usage string) (ttl string, pos []string) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--ttl":
			// A following flag means the value was forgotten: no valid duration starts with `-`, and
			// swallowing the flag would send it to the server as a TTL and come back a puzzling 400.
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
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
