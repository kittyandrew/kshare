// kshare: CLI for kshare. See
// .claude/rules/001-architecture.md for component topology;
// docs/deployment.md for production wiring.
//
//	kshare auth login [--server URL]   # Zitadel device-flow login
//	kshare auth logout                 # clear cached tokens
//	kshare auth status                 # show current login state
//	kshare <file> [--ttl X]            # upload (default verb)
//	kshare replace <slug> <file>       # replace existing slug's content
//	kshare ls [--json]                 # list uploads
//	kshare rm <slug>                   # delete by slug
//	kshare --help
//
// Tokens persist at ~/.config/kshare/auth.json (mode 0600), refreshed
// transparently on expiry.
package main

import (
	"errors"
	"fmt"
	"os"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		usage()
		os.Exit(1)
	}

	switch args[0] {
	case "-h", "--help", "help":
		usage()
		return
	case "auth":
		runAuth(args[1:])
		return
	case "ls":
		runList(args[1:])
		return
	case "rm":
		runDelete(args[1:])
		return
	case "replace":
		runReplace(args[1:])
		return
	}

	// Default verb: upload. The first positional argument is the path
	// to the file. `--ttl <dur>` is the only flag.
	runUpload(args)
}

// runAuth dispatches `kshare auth <subcommand>`. Three subs:
// login / logout / status. Anything else (including missing) prints
// usage.
func runAuth(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: kshare auth (login [--server URL] | logout | status)")
		os.Exit(1)
	}
	switch args[0] {
	case "login":
		runLogin(args[1:])
	case "logout":
		runLogout()
	case "status":
		runStatus()
	case "-h", "--help":
		fmt.Fprintln(os.Stderr, "usage: kshare auth (login [--server URL] | logout | status)")
	default:
		fail("kshare auth: unknown subcommand %q", args[0])
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `kshare -- OIDC-gated file share CLI

Usage:
  kshare auth login [--server URL]      Zitadel device-flow login
                                        (--server defaults to
                                        http://localhost:6980)
  kshare auth logout                    Clear cached tokens
  kshare auth status                    Show current login state
  kshare <file> [--ttl 24h]             Upload a file (default verb)
  kshare replace <slug> <file> [--ttl]  Replace an existing slug's content (same URL)
  kshare ls [--json]                    List uploads
  kshare rm <slug>                      Delete an upload

TTL is optional on the CLI; if omitted, the server applies its
KSHARE_DEFAULT_TTL (7d by default). Must be within
[KSHARE_MIN_TTL, KSHARE_MAX_TTL].

Environment (required for `+"`kshare auth login`"+`):
  KSHARE_OIDC_ISSUER      Zitadel base URL
  KSHARE_OIDC_AUDIENCE    Zitadel project ID
  KSHARE_OIDC_CLIENT_ID   kshare-cli Native app client ID

After `+"`kshare auth login`"+`, these + the server URL are persisted to
~/.config/kshare/auth.json. Subsequent commands use the persisted
values; no env required.`)
}

// fail prints a short error to stderr and exits 1. Used for
// user-facing errors that don't warrant a stack trace.
func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

// failRequest is the exit path for any error returned from an HTTP
// request to the kshared server. It distinguishes "no usable
// credentials" (route the user to `kshare auth login`) from generic
// errors (network, 5xx, decode) so the message points at the right
// next step.
func failRequest(err error) {
	if errors.Is(err, errNotLoggedIn) {
		fmt.Fprintln(os.Stderr, "kshare: not logged in; run `kshare auth login`")
		os.Exit(1)
	}
	fail("kshare: %v", err)
}
