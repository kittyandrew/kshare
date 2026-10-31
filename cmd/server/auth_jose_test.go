package main

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/zitadel/oidc/v3/pkg/oidc"
)

func TestAuthenticatorVerifyRSA(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	publicKey := jose.JSONWebKey{Key: &key.PublicKey, KeyID: "test-key", Algorithm: "RS256", Use: "sig"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]string{"issuer": "http://" + r.Host, "jwks_uri": "http://" + r.Host + "/keys"})
		case "/keys":
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{publicKey}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	auth, err := newAuthenticator(t.Context(), server.URL, "test-audience")
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", publicKey.KeyID))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"iss": server.URL, "aud": "test-audience", "sub": "test-user",
		"exp": time.Now().Add(time.Hour).Unix(), "roles": []string{roleUpload},
	})
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signed.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	got, err := auth.verify(t.Context(), "Bearer "+token)
	if err != nil {
		t.Fatal(err)
	}
	if got.Subject != "test-user" || !got.HasRole(roleUpload) {
		t.Fatalf("unexpected verified claims: %+v", got)
	}

	// The parser must bound nesting before it reaches signature verification.
	header := `{"alg":"RS256","kid":"test-key","extra":` + strings.Repeat("[", 10001) + "0" + strings.Repeat("]", 10001) + "}"
	parts := strings.Split(token, ".")
	parts[0] = base64.RawURLEncoding.EncodeToString([]byte(header))
	if _, err := auth.verify(t.Context(), "Bearer "+strings.Join(parts, ".")); !errors.Is(err, oidc.ErrParse) {
		t.Fatalf("expected header parsing rejection, got %v", err)
	}
}
