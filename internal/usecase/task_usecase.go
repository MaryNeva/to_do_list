package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"to-do-list/internal/apperr"
	"to-do-list/internal/domain"
)

type TaskConfig struct {
	Timeout              time.Duration
	MaxTitleLength       int
	MaxDescriptionLength int
}

type TaskUseCase struct {
	repo   domain.TaskRepository
	cfg    TaskConfig
	logger *slog.Logger
}

func NewTaskUseCase(repo domain.TaskRepository, cfg TaskConfig, logger *slog.Logger) *TaskUseCase {
	return &TaskUseCase{repo: repo, cfg: cfg, logger: logger}
}

var _ domain.TaskService = (*TaskUseCase)(nil)

func (uc *TaskUseCase) Create(ctx context.Context, creatorID int64, title, description string) (domain.Task, error) {
	ctx, cancel := context.WithTimeout(ctx, uc.cfg.Timeout)
	defer cancel()

	title = strings.TrimSpace(title)
	description = strings.TrimSpace(description)

	if err := uc.validateTaskFields(title, description); err != nil {
		return domain.Task{}, err
	}

	task := domain.Task{
		Title:       title,
		Description: description,
		Status:      domain.StatusCreated,
		CreatorID:   creatorID,
	}

	created, err := uc.repo.Create(ctx, task)
	if err != nil {
		uc.logger.ErrorContext(ctx, "create task failed", "error", err, "creator_id", creatorID)
		return domain.Task{}, fmt.Errorf("create task: %w", err)
	}

	return created, nil
}

func (uc *TaskUseCase) Get(ctx context.Context, requesterID, id int64) (domain.Task, error) {
	ctx, cancel := context.WithTimeout(ctx, uc.cfg.Timeout)
	defer cancel()

	task, err := uc.repo.GetByID(ctx, id)
	if err != nil {
		return domain.Task{}, err
	}

	if task.CreatorID != requesterID {
		return domain.Task{}, apperr.ErrNotFound
	}

	return task, nil
}

func (uc *TaskUseCase) List(ctx context.Context, requesterID int64) ([]domain.Task, error) {
	ctx, cancel := context.WithTimeout(ctx, uc.cfg.Timeout)
	defer cancel()

	tasks, err := uc.repo.ListByCreator(ctx, requesterID)
	if err != nil {
		uc.logger.ErrorContext(ctx, "list tasks failed", "error", err, "creator_id", requesterID)
		return nil, fmt.Errorf("list tasks: %w", err)
	}

	return tasks, nil
}

func (uc *TaskUseCase) Update(ctx context.Context, requesterID, id int64, title, description string) (domain.Task, error) {
	ctx, cancel := context.WithTimeout(ctx, uc.cfg.Timeout)
	defer cancel()

	title = strings.TrimSpace(title)
	description = strings.TrimSpace(description)

	if err := uc.validateTaskFields(title, description); err != nil {
		return domain.Task{}, err
	}

	existing, err := uc.repo.GetByID(ctx, id)
	if err != nil {
		return domain.Task{}, err
	}
	if existing.CreatorID != requesterID {
		return domain.Task{}, apperr.ErrNotFound
	}

	existing.Title = title
	existing.Description = description

	updated, err := uc.repo.Update(ctx, existing)
	if err != nil {
		uc.logger.ErrorContext(ctx, "update task failed", "error", err, "task_id", id)
		return domain.Task{}, fmt.Errorf("update task: %w", err)
	}

	return updated, nil
}

func (uc *TaskUseCase) Delete(ctx context.Context, requesterID, id int64) error {
	ctx, cancel := context.WithTimeout(ctx, uc.cfg.Timeout)
	defer cancel()

	existing, err := uc.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if existing.CreatorID != requesterID {
		return apperr.ErrNotFound
	}

	if err := uc.repo.Delete(ctx, id); err != nil {
		uc.logger.ErrorContext(ctx, "delete task failed", "error", err, "task_id", id)
		return fmt.Errorf("delete task: %w", err)
	}

	return nil
}

const maxToggleAttempts = 50

func (uc *TaskUseCase) ToggleStatus(ctx context.Context, requesterID, id int64) (domain.Task, error) {
	ctx, cancel := context.WithTimeout(ctx, uc.cfg.Timeout)
	defer cancel()

	for attempt := 0; attempt < maxToggleAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return domain.Task{}, err
		}

		existing, err := uc.repo.GetByID(ctx, id)
		if err != nil {
			return domain.Task{}, err
		}
		if existing.CreatorID != requesterID {
			return domain.Task{}, apperr.ErrNotFound
		}

		updated, err := uc.repo.CompareAndSetStatus(ctx, id, requesterID, existing.Status, domain.NextStatus(existing.Status))
		switch {
		case err == nil:
			return updated, nil
		case errors.Is(err, apperr.ErrConflict):
			continue // someone else moved it; retry from its new status
		case errors.Is(err, apperr.ErrNotFound):
			return domain.Task{}, err
		default:
			uc.logger.ErrorContext(ctx, "toggle task status failed", "error", err, "task_id", id)
			return domain.Task{}, fmt.Errorf("toggle task status: %w", err)
		}
	}

	uc.logger.WarnContext(ctx, "toggle task status gave up after repeated conflicts", "task_id", id, "attempts", maxToggleAttempts)
	return domain.Task{}, fmt.Errorf("%w: task status changed concurrently, try again", apperr.ErrConflict)
}

func (uc *TaskUseCase) validateTaskFields(title, description string) error {
	switch {
	case title == "":
		return fmt.Errorf("%w: title is required", apperr.ErrValidation)
	case utf8.RuneCountInString(title) > uc.cfg.MaxTitleLength:
		return fmt.Errorf("%w: title must be at most %d characters", apperr.ErrValidation, uc.cfg.MaxTitleLength)
	case utf8.RuneCountInString(description) > uc.cfg.MaxDescriptionLength:
		return fmt.Errorf("%w: description must be at most %d characters", apperr.ErrValidation, uc.cfg.MaxDescriptionLength)
	default:
		return nil
	}
}
