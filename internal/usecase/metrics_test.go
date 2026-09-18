package usecase

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"to-do-list/internal/apperr"
	"to-do-list/internal/domain"
)

type recordedCleanup struct {
	outcome  string
	removed  int64
	duration time.Duration
}

type fakeMetrics struct {
	mu sync.Mutex

	auth            map[string]int
	rotation        map[string]int
	revoked         map[string]int
	revocationCalls map[string]int
	cleanups        []recordedCleanup
}

func newFakeMetrics() *fakeMetrics {
	return &fakeMetrics{
		auth:            map[string]int{},
		rotation:        map[string]int{},
		revoked:         map[string]int{},
		revocationCalls: map[string]int{},
	}
}

func (f *fakeMetrics) AuthAttempt(operation, outcome string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auth[operation+"/"+outcome]++
}

func (f *fakeMetrics) RefreshRotation(outcome string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rotation[outcome]++
}

func (f *fakeMetrics) SessionsRevoked(reason string, count int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked[reason] += int(count)
	f.revocationCalls[reason]++
}

func (f *fakeMetrics) CleanupRun(outcome string, removed int64, d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleanups = append(f.cleanups, recordedCleanup{outcome, removed, d})
}

func (f *fakeMetrics) count(m map[string]int, key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return m[key]
}

func TestOutcomeOf_SeparatesTheCallersMistakeFromTheServices(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"no error", nil, OutcomeSuccess},
		{"validation", fmt.Errorf("%w: too short", apperr.ErrValidation), OutcomeRejected},
		{"conflict", apperr.ErrConflict, OutcomeRejected},
		{"bad credentials", apperr.ErrInvalidCredentials, OutcomeRejected},
		{"unauthorized", apperr.ErrUnauthorized, OutcomeRejected},
		{"not found", apperr.ErrNotFound, OutcomeRejected},
		{"wrapped internal error", fmt.Errorf("create user: %w", errors.New("connection reset")), OutcomeFailure},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := outcomeOf(tc.err); got != tc.want {
				t.Errorf("outcomeOf(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

func TestUseCases_WorkWithoutARecorder(t *testing.T) {
	uc, _, _ := newAuthUseCaseForTest(t, "", "")

	if _, err := uc.Register(context.Background(), "mary", "mary@example.com", "password123"); err != nil {
		t.Fatalf("Register() unexpected error: %v", err)
	}
}

func TestAuthUseCase_RecordsRegistrationAndLoginOutcomes(t *testing.T) {
	metrics := newFakeMetrics()
	uc, _, _, _ := newAuthUseCaseWithRefresh(t, "", "", WithMetrics(metrics))
	ctx := context.Background()

	if _, err := uc.Register(ctx, "mary", "mary@example.com", "password123"); err != nil {
		t.Fatalf("Register() unexpected error: %v", err)
	}
	if _, err := uc.Register(ctx, "mary", "other@example.com", "password123"); err == nil {
		t.Fatal("the duplicate registration should have failed")
	}
	if _, _, err := uc.Login(ctx, "mary", "password123"); err != nil {
		t.Fatalf("Login() unexpected error: %v", err)
	}
	if _, _, err := uc.Login(ctx, "mary", "wrong-password"); err == nil {
		t.Fatal("the wrong password should have been rejected")
	}

	expected := map[string]int{
		OperationRegister + "/" + OutcomeSuccess:  1,
		OperationRegister + "/" + OutcomeRejected: 1,
		OperationLogin + "/" + OutcomeSuccess:     1,
		OperationLogin + "/" + OutcomeRejected:    1,
	}
	for key, want := range expected {
		if got := metrics.count(metrics.auth, key); got != want {
			t.Errorf("auth[%s] = %d, want %d", key, got, want)
		}
	}
	if got := metrics.count(metrics.auth, OperationLogin+"/"+OutcomeFailure); got != 0 {
		t.Errorf("a wrong password was counted as a service failure %d times", got)
	}
}

func TestAuthUseCase_RecordsRefreshOutcomesSeparately(t *testing.T) {
	metrics := newFakeMetrics()
	uc, _, _, _ := newAuthUseCaseWithRefresh(t, "", "", WithMetrics(metrics))
	ctx := context.Background()

	if _, err := uc.Register(ctx, "mary", "mary@example.com", "password123"); err != nil {
		t.Fatalf("Register() unexpected error: %v", err)
	}
	tokens, _, err := uc.Login(ctx, "mary", "password123")
	if err != nil {
		t.Fatalf("Login() unexpected error: %v", err)
	}

	if _, err := uc.Refresh(ctx, tokens.RefreshToken); err != nil {
		t.Fatalf("Refresh() unexpected error: %v", err)
	}
	if _, err := uc.Refresh(ctx, tokens.RefreshToken); err == nil {
		t.Fatal("replaying a consumed token should fail")
	}
	if _, err := uc.Refresh(ctx, "a-token-that-was-never-issued"); err == nil {
		t.Fatal("an unknown token should fail")
	}

	for outcome, want := range map[string]int{
		OutcomeSuccess: 1,
		OutcomeReuse:   1,
		OutcomeUnknown: 1,
	} {
		if got := metrics.count(metrics.rotation, outcome); got != want {
			t.Errorf("rotation[%s] = %d, want %d", outcome, got, want)
		}
	}

	if got := metrics.count(metrics.revoked, ReasonTokenReuse); got != 1 {
		t.Errorf("sessions revoked for reuse = %d, want 1", got)
	}
}

func TestAuthUseCase_RecordsLogoutOnlyWhenASessionEnded(t *testing.T) {
	metrics := newFakeMetrics()
	uc, _, _, _ := newAuthUseCaseWithRefresh(t, "", "", WithMetrics(metrics))
	ctx := context.Background()

	if _, err := uc.Register(ctx, "mary", "mary@example.com", "password123"); err != nil {
		t.Fatalf("Register() unexpected error: %v", err)
	}
	tokens, _, err := uc.Login(ctx, "mary", "password123")
	if err != nil {
		t.Fatalf("Login() unexpected error: %v", err)
	}

	if err := uc.Logout(ctx, tokens.RefreshToken); err != nil {
		t.Fatalf("Logout() unexpected error: %v", err)
	}

	if err := uc.Logout(ctx, tokens.RefreshToken); err != nil {
		t.Fatalf("second Logout() unexpected error: %v", err)
	}

	if got := metrics.count(metrics.revoked, ReasonLogout); got != 1 {
		t.Errorf("sessions revoked on logout = %d, want 1", got)
	}
}

func TestSessionCleaner_RecordsEverySweep(t *testing.T) {
	metrics := newFakeMetrics()
	repo := &countingRefreshRepo{
		fakeRefreshRepo: newFakeRefreshRepo(),
		calls:           make(chan time.Time, 4),
		cutoff:          make(chan time.Time, 4),
	}

	cleaner := NewSessionCleaner(repo, SessionCleanupConfig{
		Interval:  time.Hour,
		Retention: time.Hour,
		Timeout:   time.Second,
	}, silentLogger(), WithMetrics(metrics))

	if _, err := cleaner.CleanupOnce(context.Background()); err != nil {
		t.Fatalf("CleanupOnce() unexpected error: %v", err)
	}

	repo.err = errors.New("connection reset")
	if _, err := cleaner.CleanupOnce(context.Background()); err == nil {
		t.Fatal("a failing sweep should return its error")
	}

	metrics.mu.Lock()
	defer metrics.mu.Unlock()

	if len(metrics.cleanups) != 2 {
		t.Fatalf("recorded %d sweeps, want 2", len(metrics.cleanups))
	}
	if metrics.cleanups[0].outcome != OutcomeSuccess {
		t.Errorf("first sweep = %+v, want a success", metrics.cleanups[0])
	}
	if metrics.cleanups[1].outcome != OutcomeFailure || metrics.cleanups[1].removed != 0 {
		t.Errorf("second sweep = %+v, want failure with 0 rows", metrics.cleanups[1])
	}
}

func TestUserUseCase_RecordsSessionRevocationOnPasswordChangeOnly(t *testing.T) {
	metrics := newFakeMetrics()
	repo := newFakeUserRepo()
	hasher := testHasher(t)

	var sessionsToRevoke int64
	repo.onSessionRevocation = func(int64) int64 { return sessionsToRevoke }

	uc := NewUserUseCase(repo, hasher, UserConfig{
		Timeout:           time.Second,
		MinUsernameLength: 3,
		MaxUsernameLength: 50,
		MinPasswordLength: 8,
		DefaultPageSize:   20,
		MaxPageSize:       100,
	}, silentLogger(), WithMetrics(metrics))

	ctx := context.Background()
	hash, err := hasher.Hash("password123")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	created, err := repo.Create(ctx, domain.User{
		Username:     "mary",
		Email:        "mary@example.com",
		PasswordHash: hash,
	})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	sessionsToRevoke = 5
	if _, err := uc.Update(ctx, created.ID, "", "new@example.com", ""); err != nil {
		t.Fatalf("Update(email) unexpected error: %v", err)
	}
	if got := metrics.count(metrics.revocationCalls, ReasonPasswordChange); got != 0 {
		t.Errorf("changing an email touched the counter %d times, want 0", got)
	}

	sessionsToRevoke = 0
	if _, err := uc.Update(ctx, created.ID, "", "", "first-new-password"); err != nil {
		t.Fatalf("Update(password) unexpected error: %v", err)
	}
	if got := metrics.count(metrics.revoked, ReasonPasswordChange); got != 0 {
		t.Errorf("sessions revoked = %d, want 0 when the account had none", got)
	}

	sessionsToRevoke = 1
	if _, err := uc.Update(ctx, created.ID, "", "", "second-new-password"); err != nil {
		t.Fatalf("Update(password) unexpected error: %v", err)
	}
	if got := metrics.count(metrics.revoked, ReasonPasswordChange); got != 1 {
		t.Errorf("sessions revoked = %d, want 1", got)
	}

	sessionsToRevoke = 3
	if _, err := uc.Update(ctx, created.ID, "", "", "third-new-password"); err != nil {
		t.Fatalf("Update(password) unexpected error: %v", err)
	}
	if got := metrics.count(metrics.revoked, ReasonPasswordChange); got != 4 {
		t.Errorf("sessions revoked = %d, want 4 (1 + 3)", got)
	}
}

func TestUserUseCase_FailedPasswordChangeRecordsNoRevocation(t *testing.T) {
	metrics := newFakeMetrics()
	repo := newFakeUserRepo()
	hasher := testHasher(t)

	uc := NewUserUseCase(repo, hasher, UserConfig{
		Timeout:           time.Second,
		MinUsernameLength: 3,
		MaxUsernameLength: 50,
		MinPasswordLength: 8,
		DefaultPageSize:   20,
		MaxPageSize:       100,
	}, silentLogger(), WithMetrics(metrics))

	if _, err := uc.Update(context.Background(), 404, "", "", "brand-new-password"); err == nil {
		t.Fatal("updating a user that does not exist should fail")
	}

	if got := metrics.count(metrics.revocationCalls, ReasonPasswordChange); got != 0 {
		t.Errorf("a failed update touched the counter %d times, want 0", got)
	}
}

func TestAuthUseCase_ReuseCountsEverySessionItEnded(t *testing.T) {
	metrics := newFakeMetrics()
	uc, _, _, _ := newAuthUseCaseWithRefresh(t, "", "", WithMetrics(metrics))
	ctx := context.Background()

	if _, err := uc.Register(ctx, "mary", "mary@example.com", "password123"); err != nil {
		t.Fatalf("Register() unexpected error: %v", err)
	}

	// Three devices logged in; one of their tokens is then replayed.
	var replayed string
	for i := 0; i < 3; i++ {
		tokens, _, err := uc.Login(ctx, "mary", "password123")
		if err != nil {
			t.Fatalf("Login() unexpected error: %v", err)
		}
		replayed = tokens.RefreshToken
	}

	if _, err := uc.Refresh(ctx, replayed); err != nil {
		t.Fatalf("Refresh() unexpected error: %v", err)
	}
	if _, err := uc.Refresh(ctx, replayed); err == nil {
		t.Fatal("replaying a consumed token should fail")
	}

	// Two untouched sessions plus the replacement the first refresh issued.
	if got := metrics.count(metrics.revoked, ReasonTokenReuse); got != 3 {
		t.Errorf("sessions revoked on reuse = %d, want 3", got)
	}
	if got := metrics.count(metrics.revocationCalls, ReasonTokenReuse); got != 1 {
		t.Errorf("the counter was touched %d times, want 1", got)
	}
}

// A revocation that failed ended nothing, so reporting sessions closed would
// be a lie that hides the failure.
func TestAuthUseCase_FailedRevocationAfterReuseRecordsNothing(t *testing.T) {
	metrics := newFakeMetrics()
	uc, _, _, refresh := newAuthUseCaseWithRefresh(t, "", "", WithMetrics(metrics))
	ctx := context.Background()

	if _, err := uc.Register(ctx, "mary", "mary@example.com", "password123"); err != nil {
		t.Fatalf("Register() unexpected error: %v", err)
	}
	tokens, _, err := uc.Login(ctx, "mary", "password123")
	if err != nil {
		t.Fatalf("Login() unexpected error: %v", err)
	}
	if _, err := uc.Refresh(ctx, tokens.RefreshToken); err != nil {
		t.Fatalf("Refresh() unexpected error: %v", err)
	}

	refresh.revokeAllErr = errors.New("connection reset")

	if _, err := uc.Refresh(ctx, tokens.RefreshToken); err == nil {
		t.Fatal("replaying a consumed token should fail")
	}

	if got := metrics.count(metrics.revocationCalls, ReasonTokenReuse); got != 0 {
		t.Errorf("a failed revocation touched the counter %d times, want 0", got)
	}
	// The replay itself is still reported, so the signal is not lost.
	if got := metrics.count(metrics.rotation, OutcomeReuse); got != 1 {
		t.Errorf("reuse rotations = %d, want 1", got)
	}
}
