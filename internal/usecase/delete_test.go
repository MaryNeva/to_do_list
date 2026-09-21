package usecase

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"to-do-list/internal/apperr"
	"to-do-list/internal/domain"
)

type deletedMeanwhileTaskRepo struct{ *fakeTaskRepo }

func (r deletedMeanwhileTaskRepo) Delete(context.Context, int64) error { return apperr.ErrNotFound }

func TestTaskUseCase_Delete_ConcurrentDeleteIsNotFoundAndNotLoggedAsError(t *testing.T) {
	var logs bytes.Buffer
	repo := newFakeTaskRepo()
	uc := NewTaskUseCase(deletedMeanwhileTaskRepo{repo}, testTaskConfig(), slog.New(slog.NewTextHandler(&logs, nil)))

	task, err := uc.Create(context.Background(), actor(1), "title", "")
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}

	if err := uc.Delete(context.Background(), actor(1), task.ID); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("Delete() = %v, want apperr.ErrNotFound", err)
	}
	if strings.Contains(logs.String(), "level=ERROR") {
		t.Errorf("a missing task was logged as an error:\n%s", logs.String())
	}
}

func TestUserUseCase_Delete_MissingUserIsNotFoundAndNotLoggedAsError(t *testing.T) {
	var logs bytes.Buffer
	uc := NewUserUseCase(newFakeUserRepo(), testHasher(t), testUserConfig(), slog.New(slog.NewTextHandler(&logs, nil)))

	if err := uc.Delete(context.Background(), domain.Claims{IsAdmin: true}, 404); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("Delete() = %v, want apperr.ErrNotFound", err)
	}
	if strings.Contains(logs.String(), "level=ERROR") {
		t.Errorf("a missing user was logged as an error:\n%s", logs.String())
	}
}
