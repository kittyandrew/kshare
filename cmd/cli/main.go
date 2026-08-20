// kshare: the CLI. Component topology in .claude/rules/001-architecture.md, production wiring in
// docs/deployment.md. The command list lives in usage() below, once.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/zitadel/oidc/v3/pkg/oidc"
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

	// Default verb: upload. The first positional argument is the file path; `--ttl <dur>` is the only flag.
	runUpload(args)
}

// runAuth dispatches login / logout / status; anything else, including nothing, prints usage.
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
	fmt.Fprintln(os.Stderr, `kshare: OIDC-gated file share CLI

Usage:
  kshare auth login [--server URL]      Zitadel device-flow login
                                        (--server defaults to $KSHARE_SERVER,
                                        else http://localhost:6980)
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

Optional:
  KSHARE_SERVER           Default for --server at login time

After `+"`kshare auth login`"+`, these + the server URL are persisted to
~/.config/kshare/auth.json. Subsequent commands use the persisted
values; no env required.`)
}

// fail prints a short error to stderr and exits 1, for user-facing errors that don't warrant a trace.
func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

// failRequest is the exit path for any error from a request to kshared. Each sentinel routes the user to a
// different next step: errNotLoggedIn says "log in", errRefreshFailed surfaces the upstream cause via
// printRefreshError, and anything else (network, 5xx, decode) prints as-is. Both sentinels are documented at
// their declaration in auth.go.
func failRequest(err error) {
	if errors.Is(err, errNotLoggedIn) {
		fmt.Fprintln(os.Stderr, "kshare: not logged in; run `kshare auth login`")
		os.Exit(1)
	}
	if errors.Is(err, errRefreshFailed) {
		printRefreshError(err)
		fmt.Fprintln(os.Stderr, "kshare: fix the upstream cause and re-run; if unsure, try `kshare auth login`")
		os.Exit(1)
	}
	fail("kshare: %v", err)
}

// printRefreshError surfaces the upstream cause of an errRefreshFailed, so runStatus and failRequest emit
// the same shape whichever subcommand tripped it.
//
// Prefer *oidc.Error, the typed upstream error from refresh-grant rejections: it formats as
// "ErrorType=X Description=Y" and carries the canonical Zitadel reason. For non-OIDC paths (a server-side
// 401 wrapped in doJSON) iterate the joined chain instead, so every line gets the "kshare: " prefix;
// errors.Join's own Error() joins with bare newlines, which reads oddly against other prefixed lines.
func printRefreshError(err error) {
	var oidcErr *oidc.Error
	if errors.As(err, &oidcErr) {
		fmt.Fprintf(os.Stderr, "kshare: auth rejected: %s\n", oidcErr)
		return
	}
	for line := range strings.SplitSeq(err.Error(), "\n") {
		if line == "" {
			continue
		}
		fmt.Fprintf(os.Stderr, "kshare: %s\n", line)
	}
}
