package token

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "test-secret-that-is-long-enough-32b"

func newTestIssuer(t *testing.T) *Issuer {
	t.Helper()
	iss, err := NewIssuer(testSecret, "insightflow", "insightflow-web")
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	return iss
}

func TestIssueVerifyRoundTrip(t *testing.T) {
	iss := newTestIssuer(t)
	raw, err := iss.Issue("11111111-1111-1111-1111-111111111111")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	claims, err := iss.Verify(raw)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("subject = %q", claims.Subject)
	}
	if claims.ID == "" {
		t.Fatal("jti claim missing")
	}
}

func TestVerifyRejectsWrongKey(t *testing.T) {
	iss := newTestIssuer(t)
	raw, _ := iss.Issue("user-1")

	other, _ := NewIssuer("another-secret-that-is-32-bytes!!", "insightflow", "insightflow-web")
	if _, err := other.Verify(raw); err == nil {
		t.Fatal("token accepted under the wrong secret")
	}
}

func TestVerifyRejectsWrongIssuerAndAudience(t *testing.T) {
	iss := newTestIssuer(t)
	raw, _ := iss.Issue("user-1")

	wrongIss, _ := NewIssuer(testSecret, "evil", "insightflow-web")
	if _, err := wrongIss.Verify(raw); err == nil {
		t.Fatal("token accepted with the wrong issuer")
	}
	wrongAud, _ := NewIssuer(testSecret, "insightflow", "other-app")
	if _, err := wrongAud.Verify(raw); err == nil {
		t.Fatal("token accepted with the wrong audience")
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	iss := newTestIssuer(t)
	past := time.Now().Add(-time.Hour)
	iss.now = func() time.Time { return past }
	raw, _ := iss.Issue("user-1")

	fresh := newTestIssuer(t)
	if _, err := fresh.Verify(raw); err == nil {
		t.Fatal("expired token was accepted")
	}
}

func TestVerifyRejectsAlgNone(t *testing.T) {
	// Forge a token that claims alg=none; it must not be trusted.
	none := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.RegisteredClaims{
		Subject:   "attacker",
		Issuer:    "insightflow",
		Audience:  jwt.ClaimStrings{"insightflow-web"},
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	})
	raw, err := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none: %v", err)
	}
	if _, err := newTestIssuer(t).Verify(raw); err == nil {
		t.Fatal("alg=none token was accepted")
	}
}

func TestNewIssuerRejectsShortSecret(t *testing.T) {
	if _, err := NewIssuer(strings.Repeat("a", 31), "iss", "aud"); err == nil {
		t.Fatal("expected rejection of a short secret")
	}
}
