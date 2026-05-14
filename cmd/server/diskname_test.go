package main

import "testing"

func TestSanitiseExt(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{".html", ".html"},
		{".HTML", ".HTML"},
		{".tar.gz", ".tar.gz"},
		{".png-1", ".png-1"},
		{".", ""},                               // leading-dot only (filepath.Ext("foo..") returns ".")
		{"/etc", ""},                            // path separator
		{".\\evil", ""},                         // backslash
		{".verylongextensionthatistoolong", ""}, // > 16 chars
		{".A1_-.", ".A1_-."},                    // every allowed char
	}
	for _, c := range cases {
		if got := sanitiseExt(c.in); got != c.want {
			t.Errorf("sanitiseExt(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
