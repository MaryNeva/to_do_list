package password

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func testHasher(t *testing.T) *Hasher {
	t.Helper()
	h, err := NewHasher(bcrypt.MinCost)
	if err != nil {
		t.Fatalf("NewHasher() unexpected error: %v", err)
	}
	return h
}

func TestNewHasher_RejectsCostOutsideBcryptRange(t *testing.T) {
	for _, cost := range []int{bcrypt.MinCost - 1, bcrypt.MaxCost + 1, 0, -1} {
		if _, err := NewHasher(cost); err == nil {
			t.Errorf("NewHasher(%d) should return an error", cost)
		}
	}
}

func TestNewHasher_AcceptsConfiguredCost(t *testing.T) {
	h, err := NewHasher(12)
	if err != nil {
		t.Fatalf("NewHasher(12) unexpected error: %v", err)
	}
	if h.Cost() != 12 {
		t.Errorf("Cost() = %d, want 12", h.Cost())
	}
}

func TestHasher_CostIsUsedForTheHash(t *testing.T) {
	h := testHasher(t)

	hash, err := h.Hash("some-password")
	if err != nil {
		t.Fatalf("Hash() unexpected error: %v", err)
	}

	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil {
		t.Fatalf("bcrypt.Cost() unexpected error: %v", err)
	}
	if cost != h.Cost() {
		t.Errorf("hash was produced at cost %d, want the hasher's %d", cost, h.Cost())
	}
}

func TestHashAndVerify(t *testing.T) {
	h := testHasher(t)

	hash, err := h.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash() unexpected error: %v", err)
	}
	if hash == "" {
		t.Fatal("Hash() returned an empty hash")
	}
	if hash == "correct horse battery staple" {
		t.Fatal("Hash() returned the plaintext password unchanged")
	}

	if err := Verify(hash, "correct horse battery staple"); err != nil {
		t.Errorf("Verify() with the correct password returned an error: %v", err)
	}
}

func TestVerify_WrongPassword(t *testing.T) {
	h := testHasher(t)

	hash, err := h.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash() unexpected error: %v", err)
	}

	if err := Verify(hash, "wrong password"); err == nil {
		t.Error("Verify() with the wrong password should return an error")
	}
}

func TestMatches(t *testing.T) {
	h := testHasher(t)

	hash, err := h.Hash("s3cret-p4ss")
	if err != nil {
		t.Fatalf("Hash() unexpected error: %v", err)
	}

	if !Matches(hash, "s3cret-p4ss") {
		t.Error("Matches() should be true for the correct password")
	}
	if Matches(hash, "not-the-password") {
		t.Error("Matches() should be false for the wrong password")
	}
	if Matches("not-a-bcrypt-hash", "anything") {
		t.Error("Matches() should be false for a malformed hash rather than panicking")
	}
}

func TestHash_EmptyPassword(t *testing.T) {
	h := testHasher(t)

	if _, err := h.Hash(""); err == nil {
		t.Error("Hash(\"\") should return an error")
	}
}

func TestHash_TooLong(t *testing.T) {
	h := testHasher(t)

	tooLong := strings.Repeat("a", MaxLength+1)
	if _, err := h.Hash(tooLong); err == nil {
		t.Error("Hash() with a password longer than MaxLength should return bcrypt.ErrPasswordTooLong")
	}
}

func TestHash_ProducesDifferentSaltsEachTime(t *testing.T) {
	h := testHasher(t)

	h1, err := h.Hash("same-password")
	if err != nil {
		t.Fatalf("Hash() unexpected error: %v", err)
	}
	h2, err := h.Hash("same-password")
	if err != nil {
		t.Fatalf("Hash() unexpected error: %v", err)
	}
	if h1 == h2 {
		t.Error("Hash() should salt each call, producing different hashes for the same input")
	}
}
