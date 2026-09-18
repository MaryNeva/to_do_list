package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"to-do-list/internal/domain"
)

type countingRefreshRepo struct {
	*fakeRefreshRepo
	calls  chan time.Time
	cutoff chan time.Time
	err    error
}

func (r *countingRefreshRepo) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	select {
	case r.cutoff <- before:
	default:
	}
	select {
	case r.calls <- time.Now():
	default:
	}
	if r.err != nil {
		return 0, r.err
	}
	return r.fakeRefreshRepo.DeleteExpired(ctx, before)
}

func newCleanerForTest(t *testing.T, interval, retention time.Duration) (*SessionCleaner, *countingRefreshRepo) {
	t.Helper()
	repo := &countingRefreshRepo{
		fakeRefreshRepo: newFakeRefreshRepo(),
		calls:           make(chan time.Time, 8),
		cutoff:          make(chan time.Time, 8),
	}
	cleaner := NewSessionCleaner(repo, SessionCleanupConfig{
		Interval:  interval,
		Retention: retention,
		Timeout:   time.Second,
	}, silentLogger())
	return cleaner, repo
}

func TestSessionCleaner_StopsWhenContextIsCancelled(t *testing.T) {
	cleaner, _ := newCleanerForTest(t, time.Hour, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan struct{})
	go func() {
		cleaner.Run(ctx)
		close(returned)
	}()

	cancel()

	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not return after its context was cancelled")
	}
}

func TestSessionCleaner_SweepsOnEveryTick(t *testing.T) {
	cleaner, repo := newCleanerForTest(t, 5*time.Millisecond, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go cleaner.Run(ctx)

	for i := 0; i < 2; i++ {
		select {
		case <-repo.calls:
		case <-time.After(2 * time.Second):
			t.Fatalf("sweep %d never happened", i+1)
		}
	}
}

func TestSessionCleaner_KeepsRowsNeededForReuseDetection(t *testing.T) {
	const retention = 24 * time.Hour
	cleaner, repo := newCleanerForTest(t, time.Hour, retention)

	justExpired := domain.RefreshToken{UserID: 1, TokenHash: "recent", ExpiresAt: time.Now().Add(-time.Minute)}
	longGone := domain.RefreshToken{UserID: 1, TokenHash: "ancient", ExpiresAt: time.Now().Add(-2 * retention)}
	repo.Create(context.Background(), justExpired, 0)
	repo.Create(context.Background(), longGone, 0)

	removed, err := cleaner.CleanupOnce(context.Background())
	if err != nil {
		t.Fatalf("CleanupOnce(): %v", err)
	}
	if removed != 1 {
		t.Errorf("removed %d rows, want only the one past the retention window", removed)
	}

	if _, err := repo.GetByHash(context.Background(), "recent"); err != nil {
		t.Error("a token that expired within the retention window must be kept for reuse detection")
	}
	if _, err := repo.GetByHash(context.Background(), "ancient"); err == nil {
		t.Error("a token long past the retention window should have been deleted")
	}

	select {
	case cutoff := <-repo.cutoff:
		if !cutoff.Before(time.Now()) {
			t.Error("the cutoff should be in the past by the retention period")
		}
	default:
		t.Error("DeleteExpired was never called")
	}
}

func TestSessionCleaner_SurvivesASweepError(t *testing.T) {
	cleaner, repo := newCleanerForTest(t, 5*time.Millisecond, time.Hour)
	repo.err = errors.New("database is unhappy")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	returned := make(chan struct{})
	go func() {
		cleaner.Run(ctx)
		close(returned)
	}()

	for i := 0; i < 2; i++ {
		select {
		case <-repo.calls:
		case <-time.After(2 * time.Second):
			t.Fatal("the cleaner stopped sweeping after an error")
		}
	}

	cancel()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not return after cancellation")
	}
}
