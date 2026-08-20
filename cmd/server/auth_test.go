package main

import "testing"

func TestBearerFrom(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"Bearer abc", "abc"},
		{"bearer xyz", "xyz"},
		{"BEARER token", "token"},
		{"Basic blah", ""},
		{"abc", ""},
		{"Bearer    spaced   ", "spaced"},
	}
	for _, c := range cases {
		if got := bearerFrom(c.in); got != c.want {
			t.Errorf("bearerFrom(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseRoles(t *testing.T) {
	project := "proj123"
	extra := map[string]any{
		"urn:zitadel:iam:org:project:proj123:roles": map[string]any{
			"upload": map[string]any{"org-1": "example.com"},
			"admin":  map[string]any{},
		},
		"urn:zitadel:iam:org:project:other:roles": map[string]any{
			"other-role": map[string]any{},
		},
		"unrelated_claim": "ignored",
	}
	got := parseRoles(extra, project)
	if _, ok := got["upload"]; !ok {
		t.Error("missing 'upload' role")
	}
	if _, ok := got["admin"]; !ok {
		t.Error("missing 'admin' role")
	}
	if _, ok := got["other-role"]; ok {
		t.Error("role from wrong project leaked in")
	}
	if len(got) != 2 {
		t.Errorf("expected 2 roles, got %d (%v)", len(got), got)
	}
}

func TestParseRoles_NilExtra(t *testing.T) {
	got := parseRoles(nil, "any-project")
	if len(got) != 0 {
		t.Errorf("nil extra should yield empty map, got %v", got)
	}
}

func TestParseRoles_MissingClaim(t *testing.T) {
	got := parseRoles(map[string]any{"other": "stuff"}, "proj123")
	if len(got) != 0 {
		t.Errorf("missing project-roles claim should yield empty map, got %v", got)
	}
}

// HasRole is the authorization decision authenticator.require gates on; the wrapper around it is three
// lines of status mapping that only a real signed token can exercise.
func TestClaims_HasRole(t *testing.T) {
	c := &claims{Subject: "u", Roles: map[string]struct{}{roleUpload: {}}}
	if !c.HasRole(roleUpload) {
		t.Errorf("granted role not reported")
	}
	if c.HasRole("admin") {
		t.Errorf("ungranted role reported as held")
	}
	empty := &claims{Subject: "u"}
	if empty.HasRole(roleUpload) {
		t.Errorf("nil role set reported as holding %q", roleUpload)
	}
}
