package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"text/tabwriter"
	"time"

	"github.com/kittyandrew/kshare/internal/api"
)

// runList implements `kshare ls [--json]`, where --json dumps the raw API response for piping through jq.
func runList(args []string) {
	jsonOut := false
	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		case "-h", "--help":
			fmt.Fprintln(os.Stderr, "usage: kshare ls [--json]")
			os.Exit(0)
		default:
			fail("kshare ls: unknown argument %q", a)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, _, err := newRequest(ctx, http.MethodGet, "/api/files", nil)
	if err != nil {
		failRequest(err)
	}
	var rows []api.Upload
	if err := doJSON(req, &rows); err != nil {
		failRequest(err)
	}

	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rows); err != nil {
			fail("kshare ls: encode: %v", err)
		}
		return
	}

	if len(rows) == 0 {
		fmt.Fprintln(os.Stderr, "kshare: no uploads")
		return
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SLUG\tORIGINAL\tSIZE\tEXPIRES_IN")
	now := time.Now()
	for _, r := range rows {
		original := r.OriginalFilename
		if original == "" {
			original = r.Slug + r.Extension // original_filename can be "": omitted or sanitised away
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			r.Slug,
			original,
			humanBytes(r.Size),
			humanUntil(now, r.ExpiresAt),
		)
	}
	_ = tw.Flush()
}

// humanBytes renders a byte count as a short human string. Bounded at exp 5 (E = exabytes); int64 maxes out
// just under 8 EiB, so the suffix table can't be indexed off the end.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	const suffixes = "KMGTPE"
	div, exp := int64(unit), 0
	for n2 := n / unit; n2 >= unit && exp < len(suffixes)-1; n2 /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%c", float64(n)/float64(div), suffixes[exp])
}

// humanUntil renders time-to-expiry. Past-expiry rows read "expired"; the sweeper reaps them next tick.
func humanUntil(now, t time.Time) string {
	d := t.Sub(now)
	if d <= 0 {
		return "expired"
	}
	if d >= 48*time.Hour {
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
	if d >= time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	if d >= time.Minute {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}
