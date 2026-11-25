package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"insightflow/api/internal/config"
)

const testClientID = "test-client-id"

// fakeOIDCProvider is a minimal OIDC issuer for exercising the callback.
type fakeOIDCProvider struct {
	server  *httptest.Server
	key     *rsa.PrivateKey
	idToken string
}

func newFakeOIDC(t *testing.T) *fakeOIDCProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	p := &fakeOIDCProvider{key: key}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                                p.server.URL,
			"authorization_endpoint":                p.server.URL + "/auth",
			"token_endpoint":                        p.server.URL + "/token",
			"jwks_uri":                              p.server.URL + "/keys",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"keys": []map[string]any{{
			"kty": "RSA", "kid": "test", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(p.key.N.Bytes()),
			"e": "AQAB",
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"access_token": "google-access-token", "token_type": "Bearer",
			"expires_in": 3600, "id_token": p.idToken,
		})
	})
	p.server = httptest.NewServer(mux)
	t.Cleanup(p.server.Close)
	return p
}

func (p *fakeOIDCProvider) signIDToken(t *testing.T, nonce, email string, verified bool) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss":            p.server.URL,
		"aud":            testClientID,
		"sub":            "google-subject-1",
		"email":          email,
		"email_verified": verified,
		"name":           "Google User",
		"nonce":          nonce,
		"iat":            time.Now().Unix(),
		"exp":            time.Now().Add(time.Hour).Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = "test"
	raw, err := tok.SignedString(p.key)
	if err != nil {
		t.Fatalf("sign id token: %v", err)
	}
	return raw
}

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func newTestGoogle(t *testing.T, p *fakeOIDCProvider) *GoogleOAuth {
	t.Helper()
	g, err := newGoogleOAuth(context.Background(), &config.Config{
		GoogleClientID:     testClientID,
		GoogleClientSecret: "secret",
		GoogleRedirectURL:  "http://localhost/callback",
	}, p.server.URL)
	if err != nil {
		t.Fatalf("newGoogleOAuth: %v", err)
	}
	return g
}

func TestGoogleExchangeValidToken(t *testing.T) {
	p := newFakeOIDC(t)
	g := newTestGoogle(t, p)
	p.idToken = p.signIDToken(t, "nonce-1", "user@example.com", true)

	identity, err := g.Exchange(context.Background(), "auth-code", "verifier", "nonce-1")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if identity.Subject != "google-subject-1" || identity.Email != "user@example.com" || !identity.EmailVerified {
		t.Fatalf("identity = %+v", identity)
	}
}

func TestGoogleExchangeRejectsTamperedToken(t *testing.T) {
	p := newFakeOIDC(t)
	g := newTestGoogle(t, p)
	tampered := p.signIDToken(t, "nonce-1", "user@example.com", true)
	// Corrupt the signature segment.
	parts := strings.Split(tampered, ".")
	parts[2] = parts[2][:len(parts[2])-4] + "AAAA"
	p.idToken = strings.Join(parts, ".")

	if _, err := g.Exchange(context.Background(), "auth-code", "verifier", "nonce-1"); err == nil {
		t.Fatal("tampered id_token was accepted")
	}
}

func TestGoogleExchangeRejectsUnverifiedEmail(t *testing.T) {
	p := newFakeOIDC(t)
	g := newTestGoogle(t, p)
	p.idToken = p.signIDToken(t, "nonce-1", "user@example.com", false)

	if _, err := g.Exchange(context.Background(), "auth-code", "verifier", "nonce-1"); err != ErrOAuthEmailUnverified {
		t.Fatalf("err = %v, want ErrOAuthEmailUnverified", err)
	}
}

func TestGoogleExchangeRejectsWrongNonce(t *testing.T) {
	p := newFakeOIDC(t)
	g := newTestGoogle(t, p)
	p.idToken = p.signIDToken(t, "other-nonce", "user@example.com", true)

	if _, err := g.Exchange(context.Background(), "auth-code", "verifier", "nonce-1"); err != ErrOAuthStateInvalid {
		t.Fatalf("err = %v, want ErrOAuthStateInvalid", err)
	}
}

func TestGoogleAuthCodeURLUsesPKCE(t *testing.T) {
	p := newFakeOIDC(t)
	g := newTestGoogle(t, p)
	url := g.AuthCodeURL("state-1", "verifier-1", "nonce-1")
	for _, want := range []string{"code_challenge_method=S256", "nonce=nonce-1", "state=state-1", "scope=openid+email+profile"} {
		if !strings.Contains(url, want) {
			t.Fatalf("AuthCodeURL missing %q: %s", want, url)
		}
	}
}

func TestCompleteGoogleLoginLinksVerifiedAccount(t *testing.T) {
	svc, fm, _ := newTestService(t)
	ctx := context.Background()
	email := uniqueEmail("google-link")
	existing := registerAndVerify(t, svc, fm, email)
	// Drain the mail queue so later assertions are unambiguous.
	_ = fm.Messages()

	session, err := svc.CompleteGoogleLogin(ctx, GoogleIdentity{
		Subject: "google-sub-" + uuid.NewString(), Email: email, EmailVerified: true, FullName: "Google User",
	})
	if err != nil {
		t.Fatalf("CompleteGoogleLogin: %v", err)
	}
	if session.User.ID != existing.User.ID {
		t.Fatalf("linked to %s, want existing user %s", session.User.ID, existing.User.ID)
	}
}

func TestCompleteGoogleLoginReplacesUnverifiedAccount(t *testing.T) {
	svc, _, pool := newTestService(t)
	ctx := context.Background()
	email := uniqueEmail("google-hijack")

	if _, err := svc.Register(ctx, email, "correct horse battery", "Unverified"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	session, err := svc.CompleteGoogleLogin(ctx, GoogleIdentity{
		Subject: "google-sub-" + uuid.NewString(), Email: email, EmailVerified: true, FullName: "Google User",
	})
	if err != nil {
		t.Fatalf("CompleteGoogleLogin: %v", err)
	}

	var pending int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE email = $1 AND status = 'pending_verification'", email).Scan(&pending); err != nil {
		t.Fatalf("count pending: %v", err)
	}
	if pending != 0 {
		t.Fatal("unverified account survived the Google login (pre-hijacking risk)")
	}
	if session.User.Email != email || !session.User.EmailVerified {
		t.Fatalf("session = %+v", session.User)
	}
}
