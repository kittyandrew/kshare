package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

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

func TestRequireRole_Allows(t *testing.T) {
	called := false
	h := requireRole(roleUpload, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req = req.WithContext(context.WithValue(req.Context(), claimsCtxKey{},
		&claims{Subject: "u", Roles: map[string]struct{}{roleUpload: {}}}))
	w := httptest.NewRecorder()
	h(w, req)
	if !called {
		t.Fatal("handler not called despite role match")
	}
	if w.Code != http.StatusOK {
		t.Errorf("got %d, want 200", w.Code)
	}
}

func TestRequireRole_MissingRole(t *testing.T) {
	called := false
	h := requireRole(roleUpload, func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req = req.WithContext(context.WithValue(req.Context(), claimsCtxKey{},
		&claims{Subject: "u", Roles: map[string]struct{}{"reader": {}}}))
	w := httptest.NewRecorder()
	h(w, req)
	if called {
		t.Error("handler should not be called when role missing")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("got %d, want 403", w.Code)
	}
}

func TestRequireRole_NoClaims(t *testing.T) {
	called := false
	h := requireRole(roleUpload, func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	w := httptest.NewRecorder()
	h(w, req)
	if called {
		t.Error("handler should not be called without claims")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", w.Code)
	}
}
