package password

import (
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	hash, err := Hash("correct horse battery staple")
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
	hash, err := Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash() unexpected error: %v", err)
	}

	if err := Verify(hash, "wrong password"); err == nil {
		t.Error("Verify() with the wrong password should return an error")
	}
}

func TestMatches(t *testing.T) {
	hash, err := Hash("s3cret-p4ss")
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
	if _, err := Hash(""); err == nil {
		t.Error("Hash(\"\") should return an error")
	}
}

func TestHash_TooLong(t *testing.T) {
	// bcrypt silently ignores bytes beyond 72; document + verify the
	// boundary explicitly so callers know MaxLength is meaningful.
	tooLong := strings.Repeat("a", MaxLength+1)
	if _, err := Hash(tooLong); err == nil {
		t.Error("Hash() with a password longer than MaxLength should return bcrypt.ErrPasswordTooLong")
	}
}

func TestHash_ProducesDifferentSaltsEachTime(t *testing.T) {
	h1, err := Hash("same-password")
	if err != nil {
		t.Fatalf("Hash() unexpected error: %v", err)
	}
	h2, err := Hash("same-password")
	if err != nil {
		t.Fatalf("Hash() unexpected error: %v", err)
	}
	if h1 == h2 {
		t.Error("Hash() should salt each call, producing different hashes for the same input")
	}
}
