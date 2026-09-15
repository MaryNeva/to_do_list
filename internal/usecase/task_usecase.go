package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"to-do-list/internal/apperr"
	"to-do-list/internal/domain"
)

const (
	maxTaskTitleLen = 200
	maxTaskDescLen  = 4000
)

type TaskUseCase struct {
	repo    domain.TaskRepository
	timeout time.Duration
	logger  *slog.Logger
}

func NewTaskUseCase(repo domain.TaskRepository, timeout time.Duration, logger *slog.Logger) *TaskUseCase {
	return &TaskUseCase{repo: repo, timeout: timeout, logger: logger}
}

var _ domain.TaskService = (*TaskUseCase)(nil)

func (uc *TaskUseCase) Create(ctx context.Context, creatorID int64, title, description string) (domain.Task, error) {
	ctx, cancel := context.WithTimeout(ctx, uc.timeout)
	defer cancel()

	title = strings.TrimSpace(title)
	description = strings.TrimSpace(description)

	if err := validateTaskFields(title, description); err != nil {
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
	ctx, cancel := context.WithTimeout(ctx, uc.timeout)
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
	ctx, cancel := context.WithTimeout(ctx, uc.timeout)
	defer cancel()

	tasks, err := uc.repo.ListByCreator(ctx, requesterID)
	if err != nil {
		uc.logger.ErrorContext(ctx, "list tasks failed", "error", err, "creator_id", requesterID)
		return nil, fmt.Errorf("list tasks: %w", err)
	}

	return tasks, nil
}

func (uc *TaskUseCase) Update(ctx context.Context, requesterID, id int64, title, description string) (domain.Task, error) {
	ctx, cancel := context.WithTimeout(ctx, uc.timeout)
	defer cancel()

	title = strings.TrimSpace(title)
	description = strings.TrimSpace(description)

	if err := validateTaskFields(title, description); err != nil {
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
	ctx, cancel := context.WithTimeout(ctx, uc.timeout)
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

func (uc *TaskUseCase) ToggleStatus(ctx context.Context, requesterID, id int64) (domain.Task, error) {
	ctx, cancel := context.WithTimeout(ctx, uc.timeout)
	defer cancel()

	existing, err := uc.repo.GetByID(ctx, id)
	if err != nil {
		return domain.Task{}, err
	}
	if existing.CreatorID != requesterID {
		return domain.Task{}, apperr.ErrNotFound
	}

	next := domain.NextStatus(existing.Status)
	if err := uc.repo.UpdateStatus(ctx, id, next); err != nil {
		uc.logger.ErrorContext(ctx, "toggle task status failed", "error", err, "task_id", id)
		return domain.Task{}, fmt.Errorf("toggle task status: %w", err)
	}

	return uc.repo.GetByID(ctx, id)
}

func validateTaskFields(title, description string) error {
	switch {
	case title == "":
		return fmt.Errorf("%w: title is required", apperr.ErrValidation)
	case len(title) > maxTaskTitleLen:
		return fmt.Errorf("%w: title must be at most %d characters", apperr.ErrValidation, maxTaskTitleLen)
	case len(description) > maxTaskDescLen:
		return fmt.Errorf("%w: description must be at most %d characters", apperr.ErrValidation, maxTaskDescLen)
	default:
		return nil
	}
}
