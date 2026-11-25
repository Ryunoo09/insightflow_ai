package auth

import (
	"strings"
	"testing"
)

func TestHashVerifyRoundTrip(t *testing.T) {
	h := NewHasher()
	const password = "correct horse battery staple"

	encoded, err := h.Hash(password)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$") {
		t.Fatalf("unexpected PHC prefix: %q", encoded)
	}
	if !h.Verify(encoded, password) {
		t.Fatal("Verify returned false for the correct password")
	}
	if h.Verify(encoded, password+"x") {
		t.Fatal("Verify accepted a wrong password")
	}
}

func TestHashIsSalted(t *testing.T) {
	h := NewHasher()
	first, _ := h.Hash("correct horse battery staple")
	second, _ := h.Hash("correct horse battery staple")
	if first == second {
		t.Fatal("identical passwords produced identical hashes (salt missing)")
	}
}

func TestVerifyRejectsMalformedHash(t *testing.T) {
	h := NewHasher()
	for _, encoded := range []string{"", "plaintext", "$argon2id$v=19$m=1,t=1,p=1$bad", "$2y$10$abcdef"} {
		if h.Verify(encoded, "whatever") {
			t.Fatalf("Verify accepted malformed hash %q", encoded)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	if err := ValidatePassword("short"); err == nil {
		t.Fatal("expected rejection for a short password")
	}
	if err := ValidatePassword(strings.Repeat("a", 129)); err == nil {
		t.Fatal("expected rejection for an over-long password")
	}
	if err := ValidatePassword("password123"); err == nil {
		t.Fatal("expected rejection for a common password")
	}
	if err := ValidatePassword("correct horse battery"); err != nil {
		t.Fatalf("unexpected rejection: %v", err)
	}
}

// BenchmarkHash keeps an eye on the Argon2id cost (~100-300ms on dev hardware).
func BenchmarkHash(b *testing.B) {
	h := NewHasher()
	for i := 0; i < b.N; i++ {
		if _, err := h.Hash("correct horse battery staple"); err != nil {
			b.Fatal(err)
		}
	}
}
