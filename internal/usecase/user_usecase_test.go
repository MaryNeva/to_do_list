package usecase

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"to-do-list/internal/apperr"
	"to-do-list/internal/auth/password"
	"to-do-list/internal/domain"
)

type fakeUserRepo struct {
	users               map[int64]domain.User
	nextID              int64
	onSessionRevocation func(userID int64) int64
	getByIDErr          error
}

func newFakeUserRepo() *fakeUserRepo {
	return &fakeUserRepo{users: make(map[int64]domain.User), nextID: 1}
}

func (f *fakeUserRepo) Create(_ context.Context, user domain.User) (domain.User, error) {
	for _, existing := range f.users {
		if strings.EqualFold(existing.Username, user.Username) || existing.Email == user.Email {
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
	if f.getByIDErr != nil {
		return domain.User{}, f.getByIDErr
	}
	user, ok := f.users[id]
	if !ok {
		return domain.User{}, apperr.ErrNotFound
	}
	return user, nil
}

func (f *fakeUserRepo) GetByUsername(_ context.Context, username string) (domain.User, error) {
	for _, user := range f.users {
		if strings.EqualFold(user.Username, username) {
			return user, nil
		}
	}
	return domain.User{}, apperr.ErrNotFound
}

func (f *fakeUserRepo) List(_ context.Context, page domain.PageRequest) (domain.Page[domain.User], error) {
	ids := make([]int64, 0, len(f.users))
	for id := range f.users {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	total := len(ids)
	if page.Offset >= total {
		return domain.NewPage([]domain.User{}, total, page), nil
	}
	end := page.Offset + page.Limit
	if end > total {
		end = total
	}

	result := make([]domain.User, 0, end-page.Offset)
	for _, id := range ids[page.Offset:end] {
		result = append(result, f.users[id])
	}
	return domain.NewPage(result, total, page), nil
}

func (f *fakeUserRepo) Update(_ context.Context, id int64, fields domain.UserUpdate) (domain.User, error) {
	user, ok := f.users[id]
	if !ok {
		return domain.User{}, apperr.ErrNotFound
	}
	// Mirrors the SQL COALESCE: only non-nil fields are written.
	if fields.Username != nil {
		for _, other := range f.users {
			if other.ID != id && strings.EqualFold(other.Username, *fields.Username) {
				return domain.User{}, apperr.ErrConflict
			}
		}
		user.Username = *fields.Username
	}
	if fields.Email != nil {
		user.Email = *fields.Email
	}
	if fields.PasswordHash != nil {
		user.PasswordHash = *fields.PasswordHash
		// Mirrors credentials_version = credentials_version + 1.
		user.CredentialsVersion++
	}
	user.UpdatedAt = time.Now()
	f.users[id] = user
	return user, nil
}

func (f *fakeUserRepo) UpdateAndRevokeSessions(ctx context.Context, id int64, fields domain.UserUpdate) (domain.User, int64, error) {
	user, err := f.Update(ctx, id, fields)
	if err != nil {
		return domain.User{}, 0, err
	}
	var revoked int64
	if f.onSessionRevocation != nil {
		revoked = f.onSessionRevocation(id)
	}
	return user, revoked, nil
}

func (f *fakeUserRepo) Delete(_ context.Context, id int64) error {
	if _, ok := f.users[id]; !ok {
		return apperr.ErrNotFound
	}
	delete(f.users, id)
	return nil
}

func testHasher(t *testing.T) *password.Hasher {
	t.Helper()
	h, err := password.NewHasher(bcrypt.MinCost, 2)
	if err != nil {
		t.Fatalf("password.NewHasher() unexpected error: %v", err)
	}
	return h
}

func testUserConfig() UserConfig {
	return UserConfig{
		Timeout:           time.Second,
		MinUsernameLength: 3,
		MaxUsernameLength: 50,
		MinPasswordLength: 8,
		DefaultPageSize:   20,
		MaxPageSize:       100,
	}
}

func newUserUseCaseForTest(t *testing.T) (*UserUseCase, *fakeUserRepo) {
	t.Helper()
	repo := newFakeUserRepo()
	return NewUserUseCase(repo, testHasher(t), testUserConfig(), silentLogger()), repo
}

func TestUserUseCase_Update_DoesNotRehashUnchangedPassword(t *testing.T) {
	uc, repo := newUserUseCaseForTest(t)

	hash, err := testHasher(t).Hash(context.Background(), "original-password")
	if err != nil {
		t.Fatalf("Hash() unexpected error: %v", err)
	}
	created, err := repo.Create(context.Background(), domain.User{
		Username:     "alice",
		Email:        "alice@example.com",
		PasswordHash: hash,
	})
	if err != nil {
		t.Fatalf("seed Create() unexpected error: %v", err)
	}

	updated, err := uc.Update(context.Background(), domain.Claims{IsAdmin: true}, created.ID, domain.UserEdit{Email: ptr("alice@new-domain.com")})
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}

	if updated.Email != "alice@new-domain.com" {
		t.Errorf("Update() Email = %q, want %q", updated.Email, "alice@new-domain.com")
	}
	if !hasherMatches(t, updated.PasswordHash, "original-password") {
		t.Error("Update() without a new password must not change the stored password hash")
	}
}

func TestUserUseCase_Update_ChangesPasswordWhenProvided(t *testing.T) {
	uc, repo := newUserUseCaseForTest(t)

	hash, _ := testHasher(t).Hash(context.Background(), "original-password")
	created, _ := repo.Create(context.Background(), domain.User{Username: "alice", Email: "a@example.com", PasswordHash: hash})

	updated, err := uc.Update(context.Background(), domain.Claims{IsAdmin: true}, created.ID, domain.UserEdit{Password: ptr("new-password")})
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}

	if hasherMatches(t, updated.PasswordHash, "original-password") {
		t.Error("Update() with a new password should invalidate the old one")
	}
	if !hasherMatches(t, updated.PasswordHash, "new-password") {
		t.Error("Update() with a new password should accept the new one")
	}
}

func TestUserUseCase_Update_PartialFieldsLeaveOthersUnchanged(t *testing.T) {
	uc, repo := newUserUseCaseForTest(t)
	created, _ := repo.Create(context.Background(), domain.User{Username: "alice", Email: "a@example.com", PasswordHash: "hash"})

	updated, err := uc.Update(context.Background(), domain.Claims{IsAdmin: true}, created.ID, domain.UserEdit{Username: ptr("alice2")})
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
	uc, _ := newUserUseCaseForTest(t)
	if _, err := uc.Get(context.Background(), domain.Claims{IsAdmin: true}, 123); err == nil {
		t.Error("Get() for a missing user should return an error")
	}
}

func TestUserUseCase_Update_EnforcesConfiguredLengthPolicy(t *testing.T) {
	uc, repo := newUserUseCaseForTest(t)
	cfg := testUserConfig()
	created, _ := repo.Create(context.Background(), domain.User{Username: "alice", Email: "a@example.com", PasswordHash: "hash"})

	tests := []struct {
		name string
		edit domain.UserEdit
	}{
		{"username below the configured minimum", domain.UserEdit{Username: ptr(strings.Repeat("a", cfg.MinUsernameLength-1))}},
		{"username above the configured maximum", domain.UserEdit{Username: ptr(strings.Repeat("a", cfg.MaxUsernameLength+1))}},
		{"password below the configured minimum", domain.UserEdit{Password: ptr(strings.Repeat("p", cfg.MinPasswordLength-1))}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := uc.Update(context.Background(), domain.Claims{IsAdmin: true}, created.ID, tt.edit)
			if !errors.Is(err, apperr.ErrValidation) {
				t.Errorf("Update() error = %v, want apperr.ErrValidation", err)
			}
		})
	}
}

func TestUserUseCase_Update_RejectsRenameToReservedAdminName(t *testing.T) {
	repo := newFakeUserRepo()
	cfg := testUserConfig()
	cfg.AdminUsername = "admin"
	uc := NewUserUseCase(repo, testHasher(t), cfg, silentLogger())

	created, _ := repo.Create(context.Background(), domain.User{Username: "alice", Email: "a@example.com", PasswordHash: "h"})

	for _, name := range []string{"admin", "Admin", "  ADMIN  "} {
		t.Run(name, func(t *testing.T) {
			_, err := uc.Update(context.Background(), domain.Claims{IsAdmin: true}, created.ID, domain.UserEdit{Username: &name})
			if !errors.Is(err, apperr.ErrConflict) {
				t.Errorf("Update(%q) error = %v, want apperr.ErrConflict", name, err)
			}
		})
	}

	if repo.users[created.ID].Username != "alice" {
		t.Errorf("username changed to %q despite the rejection", repo.users[created.ID].Username)
	}
}

func TestUserUseCase_Update_RenameAllowedWithoutBootstrapAdmin(t *testing.T) {
	uc, repo := newUserUseCaseForTest(t) // testUserConfig leaves AdminUsername empty
	created, _ := repo.Create(context.Background(), domain.User{Username: "alice", Email: "a@example.com", PasswordHash: "h"})

	updated, err := uc.Update(context.Background(), domain.Claims{IsAdmin: true}, created.ID, domain.UserEdit{Username: ptr("admin")})
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}
	if updated.Username != "admin" {
		t.Errorf("Username = %q, want %q", updated.Username, "admin")
	}
}

func TestUserUseCase_Update_OnlySuppliedFieldsAreSent(t *testing.T) {
	repo := &recordingUserRepo{fakeUserRepo: newFakeUserRepo()}
	uc := NewUserUseCase(repo, testHasher(t), testUserConfig(), silentLogger())
	created, _ := repo.Create(context.Background(), domain.User{Username: "alice", Email: "a@example.com", PasswordHash: "h"})

	if _, err := uc.Update(context.Background(), domain.Claims{IsAdmin: true}, created.ID, domain.UserEdit{Email: ptr("new@example.com")}); err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}

	if repo.lastFields.Email == nil || *repo.lastFields.Email != "new@example.com" {
		t.Error("the changed email should have been sent")
	}
	if repo.lastFields.Username != nil {
		t.Errorf("username was sent as %q although the caller did not supply one", *repo.lastFields.Username)
	}
	if repo.lastFields.PasswordHash != nil {
		t.Error("a password hash was sent although the caller did not supply a new password")
	}
}

type recordingUserRepo struct {
	*fakeUserRepo
	lastFields domain.UserUpdate
	updates    int
}

func (r *recordingUserRepo) Update(ctx context.Context, id int64, fields domain.UserUpdate) (domain.User, error) {
	r.lastFields = fields
	r.updates++
	return r.fakeUserRepo.Update(ctx, id, fields)
}

func (r *recordingUserRepo) UpdateAndRevokeSessions(ctx context.Context, id int64, fields domain.UserUpdate) (domain.User, int64, error) {
	r.lastFields = fields
	r.updates++
	return r.fakeUserRepo.UpdateAndRevokeSessions(ctx, id, fields)
}

func TestUserUseCase_Update_CountsCharactersNotBytes(t *testing.T) {
	cfg := testUserConfig()

	tests := []struct {
		name     string
		username string
		wantErr  bool
	}{
		{"cyrillic at the limit", strings.Repeat("и", cfg.MaxUsernameLength), false},
		{"cyrillic one over", strings.Repeat("и", cfg.MaxUsernameLength+1), true},
		{"emoji at the limit", strings.Repeat("🚀", cfg.MaxUsernameLength), false},
		{"emoji one over", strings.Repeat("🚀", cfg.MaxUsernameLength+1), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc, repo := newUserUseCaseForTest(t)
			created, _ := repo.Create(context.Background(), domain.User{Username: "alice", Email: "a@example.com", PasswordHash: "h"})

			_, err := uc.Update(context.Background(), domain.Claims{IsAdmin: true}, created.ID, domain.UserEdit{Username: &tt.username})
			if tt.wantErr {
				if !errors.Is(err, apperr.ErrValidation) {
					t.Errorf("Update(%d chars) error = %v, want apperr.ErrValidation", utf8.RuneCountInString(tt.username), err)
				}
				return
			}
			if err != nil {
				t.Errorf("Update(%d chars) unexpected error: %v", utf8.RuneCountInString(tt.username), err)
			}
		})
	}
}

// The DTO tag stops a malformed address at the HTTP edge. This is the same
// rule one layer in, where it also applies to a caller that never went
// through HTTP.
func TestUseCases_RejectAMalformedEmailWithoutHelpFromTheTransport(t *testing.T) {
	for _, tc := range []struct {
		name  string
		email string
		valid bool
	}{
		{name: "ordinary address", email: "mary@example.com", valid: true},
		{name: "subdomain and plus tag", email: "mary+todo@mail.example.co.uk", valid: true},
		{name: "no at sign", email: "mary.example.com"},
		{name: "no domain", email: "mary@"},
		{name: "no local part", email: "@example.com"},
		{name: "a display name is not an address", email: "Mary <mary@example.com>"},
		{name: "spaces", email: "mary @example.com"},
		{name: "two addresses", email: "mary@example.com, eve@example.com"},
		{name: "longer than any real address", email: strings.Repeat("a", 320) + "@example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authUC, _, _ := newAuthUseCaseForTest(t, "", "")

			_, err := authUC.Register(context.Background(), "mary", tc.email, "password123")
			if tc.valid {
				if err != nil {
					t.Fatalf("Register() with %q: %v", tc.email, err)
				}
				return
			}
			if !errors.Is(err, apperr.ErrValidation) {
				t.Errorf("Register() with %q = %v, want apperr.ErrValidation", tc.email, err)
			}
		})
	}
}

func TestUserUseCase_Update_RejectsAMalformedEmail(t *testing.T) {
	uc, repo := newUserUseCaseForTest(t)
	seeded, err := repo.Create(context.Background(), domain.User{
		Username: "mary", Email: "mary@example.com", PasswordHash: "hash",
	})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	_, err = uc.Update(context.Background(), domain.Claims{UserID: seeded.ID}, seeded.ID, domain.UserEdit{Email: ptr("not-an-email")})
	if !errors.Is(err, apperr.ErrValidation) {
		t.Errorf("Update() with a malformed email = %v, want apperr.ErrValidation", err)
	}

	stored, err := repo.GetByID(context.Background(), seeded.ID)
	if err != nil {
		t.Fatalf("GetByID(): %v", err)
	}
	if stored.Email != "mary@example.com" {
		t.Errorf("the stored email became %q; a rejected update must change nothing", stored.Email)
	}
}

func TestUserUseCase_Update_RejectsAnEditWithNothingInIt(t *testing.T) {
	repo := &recordingUserRepo{fakeUserRepo: newFakeUserRepo()}
	uc := NewUserUseCase(repo, testHasher(t), testUserConfig(), silentLogger())
	created, _ := repo.Create(context.Background(), domain.User{Username: "alice", Email: "a@example.com", PasswordHash: "h"})

	_, err := uc.Update(context.Background(), domain.Claims{IsAdmin: true}, created.ID, domain.UserEdit{})
	if !errors.Is(err, apperr.ErrValidation) {
		t.Errorf("Update() with an empty edit = %v, want apperr.ErrValidation", err)
	}
	if repo.updates != 0 {
		t.Errorf("the repository was asked to update %d time(s) for an edit that carried nothing", repo.updates)
	}
}

func TestUserUseCase_Update_RejectsAFieldThatIsPresentAndEmpty(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit domain.UserEdit
	}{
		{"empty username", domain.UserEdit{Username: ptr("")}},
		{"username of nothing but spaces", domain.UserEdit{Username: ptr("   ")}},
		{"empty email", domain.UserEdit{Email: ptr("")}},
		{"email of nothing but spaces", domain.UserEdit{Email: ptr("   ")}},
		{"empty password", domain.UserEdit{Password: ptr("")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &recordingUserRepo{fakeUserRepo: newFakeUserRepo()}
			uc := NewUserUseCase(repo, testHasher(t), testUserConfig(), silentLogger())
			created, _ := repo.Create(context.Background(), domain.User{
				Username: "alice", Email: "a@example.com", PasswordHash: "h",
			})

			if _, err := uc.Update(context.Background(), domain.Claims{IsAdmin: true}, created.ID, tc.edit); !errors.Is(err, apperr.ErrValidation) {
				t.Errorf("Update() = %v, want apperr.ErrValidation", err)
			}
			if repo.updates != 0 {
				t.Errorf("the repository was asked to update %d time(s) for a rejected edit", repo.updates)
			}

			stored, err := repo.GetByID(context.Background(), created.ID)
			if err != nil {
				t.Fatalf("GetByID(): %v", err)
			}
			if stored.Username != "alice" || stored.Email != "a@example.com" || stored.PasswordHash != "h" {
				t.Errorf("a rejected edit changed the stored user: %+v", stored)
			}
		})
	}
}

func TestUserUseCase_Update_TrimsAValueThatWasActuallySent(t *testing.T) {
	repo := &recordingUserRepo{fakeUserRepo: newFakeUserRepo()}
	uc := NewUserUseCase(repo, testHasher(t), testUserConfig(), silentLogger())
	created, _ := repo.Create(context.Background(), domain.User{Username: "alice", Email: "a@example.com", PasswordHash: "h"})

	updated, err := uc.Update(context.Background(), domain.Claims{IsAdmin: true}, created.ID, domain.UserEdit{
		Username: ptr("  bob  "),
		Email:    ptr("  bob@example.com  "),
	})
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}
	if updated.Username != "bob" {
		t.Errorf("Username = %q, want %q", updated.Username, "bob")
	}
	if updated.Email != "bob@example.com" {
		t.Errorf("Email = %q, want %q", updated.Email, "bob@example.com")
	}
}

func TestUserUseCase_Update_KeepsThePasswordExactlyAsSent(t *testing.T) {
	uc, repo := newUserUseCaseForTest(t)
	created, _ := repo.Create(context.Background(), domain.User{Username: "alice", Email: "a@example.com", PasswordHash: "h"})

	const withSpaces = "  spaced-password  "
	updated, err := uc.Update(context.Background(), domain.Claims{IsAdmin: true}, created.ID, domain.UserEdit{Password: ptr(withSpaces)})
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}
	if !hasherMatches(t, updated.PasswordHash, withSpaces) {
		t.Error("the password was stored as something other than what was sent")
	}
	if hasherMatches(t, updated.PasswordHash, strings.TrimSpace(withSpaces)) {
		t.Error("the trimmed password also opens the account; the spaces were dropped")
	}
}
