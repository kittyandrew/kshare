package api

import (
	"testing"
	"time"
)

func TestParseTTL(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		err  bool
	}{
		{"", 0, true},
		{"1h", time.Hour, false},
		{"24h", 24 * time.Hour, false},
		{"7d", 7 * 24 * time.Hour, false},
		{"1w", 7 * 24 * time.Hour, false},
		{"2w", 14 * 24 * time.Hour, false},
		{"365d", 365 * 24 * time.Hour, false},
		{"30m", 30 * time.Minute, false},
		{"1w2d3h", (7+2)*24*time.Hour + 3*time.Hour, false},
		{"2d12h", 2*24*time.Hour + 12*time.Hour, false},
		{"not-a-duration", 0, true},
		{"7d-bad", 0, true},
		// Whitespace trimming.
		{"   1h", time.Hour, false},
		{"1h   ", time.Hour, false},
		// Non-positive durations are rejected.
		{"0", 0, true},
		{"0d", 0, true},
		{"0w", 0, true},
		{"-1h", 0, true},
		{"-1d", 0, true},
	}
	for _, c := range cases {
		got, err := ParseTTL(c.in)
		if c.err {
			if err == nil {
				t.Errorf("ParseTTL(%q) = %s, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseTTL(%q) errored: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseTTL(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}
