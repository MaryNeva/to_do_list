package password

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func testHasher(t *testing.T) *Hasher {
	t.Helper()
	h, err := NewHasher(bcrypt.MinCost, 2)
	if err != nil {
		t.Fatalf("NewHasher() unexpected error: %v", err)
	}
	return h
}

func TestNewHasher_RejectsCostOutsideBcryptRange(t *testing.T) {
	for _, cost := range []int{bcrypt.MinCost - 1, bcrypt.MaxCost + 1, 0, -1} {
		if _, err := NewHasher(cost, 2); err == nil {
			t.Errorf("NewHasher(%d) should return an error", cost)
		}
	}
}

func TestNewHasher_AcceptsConfiguredCost(t *testing.T) {
	h, err := NewHasher(12, 2)
	if err != nil {
		t.Fatalf("NewHasher(12, 2) unexpected error: %v", err)
	}
	if h.Cost() != 12 {
		t.Errorf("Cost() = %d, want 12", h.Cost())
	}
}

func TestHasher_CostIsUsedForTheHash(t *testing.T) {
	h := testHasher(t)

	hash, err := h.Hash(context.Background(), "some-password")
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

	hash, err := h.Hash(context.Background(), "correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash() unexpected error: %v", err)
	}
	if hash == "" {
		t.Fatal("Hash() returned an empty hash")
	}
	if hash == "correct horse battery staple" {
		t.Fatal("Hash() returned the plaintext password unchanged")
	}

	if err := h.Verify(context.Background(), hash, "correct horse battery staple"); err != nil {
		t.Errorf("Verify() with the correct password returned an error: %v", err)
	}
}

func TestVerify_WrongPassword(t *testing.T) {
	h := testHasher(t)

	hash, err := h.Hash(context.Background(), "correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash() unexpected error: %v", err)
	}

	if err := h.Verify(context.Background(), hash, "wrong password"); !errors.Is(err, ErrMismatch) {
		t.Errorf("Verify() with the wrong password = %v, want ErrMismatch", err)
	}
}

func TestMatches(t *testing.T) {
	h := testHasher(t)

	hash, err := h.Hash(context.Background(), "s3cret-p4ss")
	if err != nil {
		t.Fatalf("Hash() unexpected error: %v", err)
	}

	if err := h.Verify(context.Background(), hash, "s3cret-p4ss"); err != nil {
		t.Errorf("Verify() with the correct password: %v", err)
	}
	if err := h.Verify(context.Background(), hash, "not-the-password"); !errors.Is(err, ErrMismatch) {
		t.Errorf("Verify() with the wrong password = %v, want ErrMismatch", err)
	}
	// A hash bcrypt cannot read is not a wrong password; see
	// TestHasher_Verify_SeparatesAWrongPasswordFromABrokenHash.
	err = h.Verify(context.Background(), "not-a-bcrypt-hash", "anything")
	if err == nil {
		t.Error("Verify() accepted a malformed hash")
	}
	if errors.Is(err, ErrMismatch) {
		t.Errorf("Verify() with a malformed hash = %v, want an error of its own", err)
	}
}

func TestHash_EmptyPassword(t *testing.T) {
	h := testHasher(t)

	if _, err := h.Hash(context.Background(), ""); err == nil {
		t.Error("Hash(\"\") should return an error")
	}
}

func TestHash_TooLong(t *testing.T) {
	h := testHasher(t)

	tooLong := strings.Repeat("a", MaxLength+1)
	if _, err := h.Hash(context.Background(), tooLong); err == nil {
		t.Error("Hash() with a password longer than MaxLength should return bcrypt.ErrPasswordTooLong")
	}
}

func TestHash_ProducesDifferentSaltsEachTime(t *testing.T) {
	h := testHasher(t)

	h1, err := h.Hash(context.Background(), "same-password")
	if err != nil {
		t.Fatalf("Hash() unexpected error: %v", err)
	}
	h2, err := h.Hash(context.Background(), "same-password")
	if err != nil {
		t.Fatalf("Hash() unexpected error: %v", err)
	}
	if h1 == h2 {
		t.Error("Hash() should salt each call, producing different hashes for the same input")
	}
}

// bcrypt is meant to be slow, so without a ceiling enough simultaneous
// logins would take the whole CPU and starve every other request.
func TestHasher_AllowsNoMoreThanTheConfiguredNumberOfSlots(t *testing.T) {
	const limit = 2

	h, err := NewHasher(bcrypt.MinCost, limit)
	if err != nil {
		t.Fatalf("NewHasher(): %v", err)
	}

	releases := make([]func(), 0, limit)
	for i := 0; i < limit; i++ {
		release, err := h.acquire(context.Background())
		if err != nil {
			t.Fatalf("acquire %d of %d: %v", i+1, limit, err)
		}
		releases = append(releases, release)
	}

	full, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := h.acquire(full); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("acquiring slot %d = %v, want it to wait and then time out", limit+1, err)
	}

	// A finished hash must hand its slot back, or the hasher would wedge
	// itself after the first burst.
	releases[0]()

	freed, cancelFreed := context.WithTimeout(context.Background(), time.Second)
	defer cancelFreed()
	release, err := h.acquire(freed)
	if err != nil {
		t.Fatalf("acquiring a released slot: %v", err)
	}
	release()

	for _, release := range releases[1:] {
		release()
	}
}

// A caller that never got into the queue has not presented a wrong password,
// and login must not report it as one.
func TestHasher_QueueTimeoutIsNotAMismatch(t *testing.T) {
	h, err := NewHasher(bcrypt.MinCost, 1)
	if err != nil {
		t.Fatalf("NewHasher(): %v", err)
	}

	hash, err := h.Hash(context.Background(), "a-password")
	if err != nil {
		t.Fatalf("Hash(): %v", err)
	}

	release, err := h.acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire(): %v", err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	if _, err := h.Hash(ctx, "a-password"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Hash() with a full queue = %v, want a deadline error", err)
	}
	if err := h.Verify(ctx, hash, "a-password"); errors.Is(err, ErrMismatch) || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Verify() with a full queue = %v, want a deadline error rather than a mismatch", err)
	}
}

// Every hash returns its slot, so a hasher stays usable after a burst.
func TestHasher_ReleasesSlotsAfterEveryCall(t *testing.T) {
	h, err := NewHasher(bcrypt.MinCost, 1)
	if err != nil {
		t.Fatalf("NewHasher(): %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := h.Hash(context.Background(), "a-password"); err != nil {
				t.Errorf("Hash(): %v", err)
			}
		}()
	}
	wg.Wait()

	if held := len(h.slots); held != 0 {
		t.Errorf("%d slots are still held after every call returned", held)
	}
}

func TestHasher_Verify_SeparatesAWrongPasswordFromABrokenHash(t *testing.T) {
	h := testHasher(t)

	good, err := h.Hash(context.Background(), "correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash(): %v", err)
	}

	for _, tc := range []struct {
		name         string
		hash         string
		plain        string
		wantMismatch bool
	}{
		{name: "the right password", hash: good, plain: "correct horse battery staple"},
		{name: "the wrong password", hash: good, plain: "not it", wantMismatch: true},
		{name: "empty stored hash", hash: "", plain: "anything"},
		{name: "not bcrypt at all", hash: "not-a-bcrypt-hash", plain: "anything"},
		{name: "truncated hash", hash: good[:len(good)-5], plain: "correct horse battery staple"},
		// Go's bcrypt takes any byte as the minor version, so "$2z$" still
		// parses; an unsupported major version and an out-of-range cost are
		// the shapes it actually refuses.
		{name: "unsupported bcrypt version", hash: "$3a$12$" + good[7:], plain: "correct horse battery staple"},
		{name: "cost outside the allowed range", hash: "$2a$99$" + good[7:], plain: "correct horse battery staple"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := h.Verify(context.Background(), tc.hash, tc.plain)

			switch {
			case tc.wantMismatch:
				if !errors.Is(err, ErrMismatch) {
					t.Errorf("Verify() = %v, want ErrMismatch", err)
				}
			case tc.hash == good:
				if err != nil {
					t.Errorf("Verify() = %v, want success", err)
				}
			default:
				if err == nil {
					t.Fatal("Verify() accepted an unusable hash")
				}
				if errors.Is(err, ErrMismatch) {
					t.Errorf("Verify() = %v; an unreadable hash must not look like a wrong password", err)
				}
			}
		})
	}
}

func TestValidHash(t *testing.T) {
	h := testHasher(t)
	good, err := h.Hash(context.Background(), "a-password")
	if err != nil {
		t.Fatalf("Hash(): %v", err)
	}

	if err := ValidHash(good); err != nil {
		t.Errorf("ValidHash() on a real hash: %v", err)
	}
	for _, bad := range []string{"", "plaintext", "$2a$", good[:10]} {
		if err := ValidHash(bad); err == nil {
			t.Errorf("ValidHash(%q) accepted a hash bcrypt cannot read", bad)
		}
	}
}
