package api

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// dwSuffixRe matches an integer followed by `d` (days) or `w` (weeks),
// which time.ParseDuration does NOT accept. We preprocess these to
// hours before delegating to the stdlib parser. Edge case: garbage
// like `not7d` would rewrite to `not168h` and then fail ParseDuration
// with a slightly confusing message; acceptable trade for compound-
// duration support (`1w2d3h`).
var dwSuffixRe = regexp.MustCompile(`(\d+)([dw])`)

// ParseTTL parses a duration string, accepting `d` (days = 24h) and
// `w` (weeks = 168h) in addition to everything time.ParseDuration
// accepts. Components can be mixed (`1w2d3h`, `2d`, `24h`).
//
// Leading/trailing whitespace is trimmed. Non-positive durations are
// rejected: `0`, `0d`, `-1h` all return an error. Callers that want a
// default on empty input should check before calling.
func ParseTTL(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("ttl: empty")
	}
	// @NOTE: dwSuffixRe.ReplaceAllStringFunc only hands the matched
	// substring to the callback, not the capture groups -- so we
	// re-run FindStringSubmatch inside to split digits + suffix.
	// One-pass alternatives are ugly enough that the duplication
	// wins for a short regex.
	expanded := dwSuffixRe.ReplaceAllStringFunc(s, func(match string) string {
		sub := dwSuffixRe.FindStringSubmatch(match)
		// sub[1] = digits, sub[2] = "d" or "w"
		n, err := strconv.Atoi(sub[1])
		if err != nil {
			return match
		}
		mult := 24
		if sub[2] == "w" {
			mult = 24 * 7
		}
		return strconv.Itoa(n*mult) + "h"
	})
	d, err := time.ParseDuration(expanded)
	if err != nil {
		return 0, fmt.Errorf("ttl %q: %w", s, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("ttl %q: must be positive", s)
	}
	return d, nil
}
