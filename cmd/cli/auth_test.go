package main

import "testing"

func TestDefaultServerURL(t *testing.T) {
	t.Run("unset falls back to local dev", func(t *testing.T) {
		t.Setenv("KSHARE_SERVER", "")
		if got := defaultServerURL(); got != localServerURL {
			t.Errorf("got %q, want %q", got, localServerURL)
		}
	})
	t.Run("env wins over the local default", func(t *testing.T) {
		t.Setenv("KSHARE_SERVER", "https://share.example.com")
		if got := defaultServerURL(); got != "https://share.example.com" {
			t.Errorf("got %q, want the env value", got)
		}
	})
}

// A bad KSHARE_SERVER must fail exactly like a bad --server: both land here before auth.json is touched.
func TestValidServerURL(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"http://localhost:6980", true},
		{"https://share.example.com", true},
		{"https://share.example.com/", true},
		{"localhost:6980", false}, // no scheme; url.Parse reads "localhost" as the scheme
		{"share.example.com", false},
		{"ftp://share.example.com", false},
		{"https://", false},
		{"", false},
		{"://nope", false},
	}
	for _, c := range cases {
		if got := validServerURL(c.in); got != c.want {
			t.Errorf("validServerURL(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
