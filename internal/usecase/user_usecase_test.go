package usecase

import (
	"context"
	"testing"
	"time"

	"to-do-list/internal/apperr"
	"to-do-list/internal/auth/password"
	"to-do-list/internal/domain"
)

type fakeUserRepo struct {
	users  map[int64]domain.User
	nextID int64
}

func newFakeUserRepo() *fakeUserRepo {
	return &fakeUserRepo{users: make(map[int64]domain.User), nextID: 1}
}

func (f *fakeUserRepo) Create(_ context.Context, user domain.User) (domain.User, error) {
	for _, existing := range f.users {
		if existing.Username == user.Username || existing.Email == user.Email {
			return domain.User{}, apperr.ErrConflict
		}
	}
	user.ID = f.nextID
	f.nextID++
	now := time.Now()
	user.CreatedAt, user.UpdatedAt = now, now
	f.users[user.ID] = user
	return user, nil
}

func (f *fakeUserRepo) GetByID(_ context.Context, id int64) (domain.User, error) {
	user, ok := f.users[id]
	if !ok {
		return domain.User{}, apperr.ErrNotFound
	}
	return user, nil
}

func (f *fakeUserRepo) GetByUsername(_ context.Context, username string) (domain.User, error) {
	for _, user := range f.users {
		if user.Username == username {
			return user, nil
		}
	}
	return domain.User{}, apperr.ErrNotFound
}

func (f *fakeUserRepo) List(_ context.Context) ([]domain.User, error) {
	var result []domain.User
	for _, user := range f.users {
		result = append(result, user)
	}
	return result, nil
}

func (f *fakeUserRepo) Update(_ context.Context, user domain.User) error {
	if _, ok := f.users[user.ID]; !ok {
		return apperr.ErrNotFound
	}
	user.UpdatedAt = time.Now()
	f.users[user.ID] = user
	return nil
}

func (f *fakeUserRepo) Delete(_ context.Context, id int64) error {
	if _, ok := f.users[id]; !ok {
		return apperr.ErrNotFound
	}
	delete(f.users, id)
	return nil
}

func newUserUseCaseForTest() (*UserUseCase, *fakeUserRepo) {
	repo := newFakeUserRepo()
	return NewUserUseCase(repo, time.Second, silentLogger()), repo
}

func TestUserUseCase_Update_DoesNotRehashUnchangedPassword(t *testing.T) {
	uc, repo := newUserUseCaseForTest()

	hash, err := password.Hash("original-password")
	if err != nil {
		t.Fatalf("password.Hash() unexpected error: %v", err)
	}
	created, err := repo.Create(context.Background(), domain.User{
		Username:     "alice",
		Email:        "alice@example.com",
		PasswordHash: hash,
	})
	if err != nil {
		t.Fatalf("seed Create() unexpected error: %v", err)
	}

	updated, err := uc.Update(context.Background(), created.ID, "", "alice@new-domain.com", "")
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}

	if updated.Email != "alice@new-domain.com" {
		t.Errorf("Update() Email = %q, want %q", updated.Email, "alice@new-domain.com")
	}
	if !password.Matches(updated.PasswordHash, "original-password") {
		t.Error("Update() without a new password must not change the stored password hash")
	}
}

func TestUserUseCase_Update_ChangesPasswordWhenProvided(t *testing.T) {
	uc, repo := newUserUseCaseForTest()

	hash, _ := password.Hash("original-password")
	created, _ := repo.Create(context.Background(), domain.User{Username: "alice", Email: "a@example.com", PasswordHash: hash})

	updated, err := uc.Update(context.Background(), created.ID, "", "", "new-password")
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}

	if password.Matches(updated.PasswordHash, "original-password") {
		t.Error("Update() with a new password should invalidate the old one")
	}
	if !password.Matches(updated.PasswordHash, "new-password") {
		t.Error("Update() with a new password should accept the new one")
	}
}

func TestUserUseCase_Update_PartialFieldsLeaveOthersUnchanged(t *testing.T) {
	uc, repo := newUserUseCaseForTest()
	created, _ := repo.Create(context.Background(), domain.User{Username: "alice", Email: "a@example.com", PasswordHash: "hash"})

	updated, err := uc.Update(context.Background(), created.ID, "alice2", "", "")
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}
	if updated.Username != "alice2" {
		t.Errorf("Update() Username = %q, want %q", updated.Username, "alice2")
	}
	if updated.Email != "a@example.com" {
		t.Errorf("Update() Email = %q, want unchanged %q", updated.Email, "a@example.com")
	}
}

func TestUserUseCase_Get_NotFound(t *testing.T) {
	uc, _ := newUserUseCaseForTest()
	if _, err := uc.Get(context.Background(), 123); err == nil {
		t.Error("Get() for a missing user should return an error")
	}
}
