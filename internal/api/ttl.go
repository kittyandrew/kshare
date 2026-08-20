package api

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// dwSuffixRe matches an integer followed by `d` (days) or `w` (weeks), which time.ParseDuration does NOT
// accept; those get preprocessed to hours before the stdlib parser sees them. Edge case: garbage like `not7d`
// rewrites to `not168h` and then fails ParseDuration with a slightly confusing message, an acceptable trade
// for compound durations like `1w2d3h`.
var dwSuffixRe = regexp.MustCompile(`(\d+)([dw])`)

// ParseTTL parses a duration string, accepting `d` (24h) and `w` (168h) on top of everything
// time.ParseDuration accepts. Components can be mixed: `1w2d3h`, `2d`, `24h`.
//
// Leading and trailing whitespace is trimmed. Non-positive durations are rejected, so `0`, `0d` and `-1h`
// all error. Callers that want a default on empty input should check before calling.
func ParseTTL(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("ttl: empty")
	}
	// @NOTE: ReplaceAllStringFunc hands the callback only the matched substring, not the capture groups, so
	// re-run FindStringSubmatch inside it to split digits from suffix. One-pass alternatives are ugly enough
	// that the duplication wins for a regex this short.
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
