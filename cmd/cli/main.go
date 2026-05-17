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
// request to the kshared server. Three branches, each routing the
// user to a different next step:
//
//   - errNotLoggedIn: no usable persisted credentials (auth.json
//     missing, or no refresh_token in it). Fix: `kshare auth login`.
//   - errRefreshFailed: auth-layer rejection that re-login won't
//     fix (IdP refused refresh grant, OR server rejected an
//     otherwise-fresh token). Surfaces the upstream error via
//     printRefreshError so the operator sees what to fix.
//   - anything else: generic network/5xx/decode. Print the err.
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

// printRefreshError surfaces the upstream cause of an
// errRefreshFailed. Single helper so runStatus and failRequest emit
// the same shape regardless of which CLI subcommand triggered the
// failure.
//
// Extraction strategy: prefer *oidc.Error (the typed upstream error
// from refresh-grant rejections) which formats as
// "ErrorType=X Description=Y" and carries the canonical Zitadel
// reason. For non-OIDC paths (e.g. server-side 401 wrapped in
// doJSON), iterate the joined-error chain so each line gets the
// "kshare: " prefix -- errors.Join's default Error() joins with
// bare newlines, which reads oddly against other prefixed lines.
func printRefreshError(err error) {
	var oidcErr *oidc.Error
	if errors.As(err, &oidcErr) {
		fmt.Fprintf(os.Stderr, "kshare: auth rejected: %s\n", oidcErr)
		return
	}
	for _, line := range strings.Split(err.Error(), "\n") {
		if line == "" {
			continue
		}
		fmt.Fprintf(os.Stderr, "kshare: %s\n", line)
	}
}
