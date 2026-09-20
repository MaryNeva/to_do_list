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
	DefaultPageSize      int
	MaxPageSize          int
}

type TaskUseCase struct {
	repo   TaskRepository
	cfg    TaskConfig
	logger *slog.Logger
}

func NewTaskUseCase(repo TaskRepository, cfg TaskConfig, logger *slog.Logger) *TaskUseCase {
	return &TaskUseCase{repo: repo, cfg: cfg, logger: logger}
}

func (uc *TaskUseCase) Create(ctx context.Context, actor domain.Claims, title, description string) (domain.Task, error) {
	creatorID, err := taskOwner(actor)
	if err != nil {
		return domain.Task{}, err
	}

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

func (uc *TaskUseCase) Get(ctx context.Context, actor domain.Claims, id int64) (domain.Task, error) {
	requesterID, err := taskOwner(actor)
	if err != nil {
		return domain.Task{}, err
	}

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

func (uc *TaskUseCase) List(ctx context.Context, actor domain.Claims, filter domain.TaskFilter) (domain.Page[domain.Task], error) {
	requesterID, err := taskOwner(actor)
	if err != nil {
		return domain.Page[domain.Task]{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, uc.cfg.Timeout)
	defer cancel()

	if filter.Status != nil && !domain.IsValidStatus(*filter.Status) {
		return domain.Page[domain.Task]{}, fmt.Errorf("%w: unknown status %q", apperr.ErrValidation, *filter.Status)
	}

	filter.Page = clampPage(filter.Page, uc.cfg.DefaultPageSize, uc.cfg.MaxPageSize)

	page, err := uc.repo.ListByCreator(ctx, requesterID, filter)
	if err != nil {
		uc.logger.ErrorContext(ctx, "list tasks failed", "error", err, "creator_id", requesterID)
		return domain.Page[domain.Task]{}, fmt.Errorf("list tasks: %w", err)
	}

	return page, nil
}

// Update applies a partial edit. Setting a status here is absolute, so a
// client that retries after a lost response lands on the status it asked for
// rather than one step further, which is what ToggleStatus cannot promise.
func (uc *TaskUseCase) Update(ctx context.Context, actor domain.Claims, id int64, update domain.TaskUpdate) (domain.Task, error) {
	requesterID, err := taskOwner(actor)
	if err != nil {
		return domain.Task{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, uc.cfg.Timeout)
	defer cancel()

	update, err = uc.normalizeUpdate(update)
	if err != nil {
		return domain.Task{}, err
	}

	updated, err := uc.repo.Update(ctx, id, requesterID, update)
	switch {
	case err == nil:
		return updated, nil
	case errors.Is(err, apperr.ErrNotFound):
		return domain.Task{}, err
	default:
		uc.logger.ErrorContext(ctx, "update task failed", "error", err, "task_id", id)
		return domain.Task{}, fmt.Errorf("update task: %w", err)
	}
}

// normalizeUpdate trims what was sent and rejects what a task cannot hold.
// An absent field is left alone; an empty description clears it, an empty
// title does not, because a task without a title has nothing to show.
func (uc *TaskUseCase) normalizeUpdate(update domain.TaskUpdate) (domain.TaskUpdate, error) {
	if update.IsEmpty() {
		return update, fmt.Errorf("%w: at least one of title, description, status must be present", apperr.ErrValidation)
	}

	if update.Title != nil {
		title := strings.TrimSpace(*update.Title)
		switch {
		case title == "":
			return update, fmt.Errorf("%w: title must not be empty", apperr.ErrValidation)
		case utf8.RuneCountInString(title) > uc.cfg.MaxTitleLength:
			return update, fmt.Errorf("%w: title must be at most %d characters", apperr.ErrValidation, uc.cfg.MaxTitleLength)
		}
		update.Title = &title
	}

	if update.Description != nil {
		description := strings.TrimSpace(*update.Description)
		if utf8.RuneCountInString(description) > uc.cfg.MaxDescriptionLength {
			return update, fmt.Errorf("%w: description must be at most %d characters", apperr.ErrValidation, uc.cfg.MaxDescriptionLength)
		}
		update.Description = &description
	}

	if update.Status != nil && !domain.IsValidStatus(*update.Status) {
		return update, fmt.Errorf("%w: unknown status %q", apperr.ErrValidation, *update.Status)
	}

	return update, nil
}

func (uc *TaskUseCase) Delete(ctx context.Context, actor domain.Claims, id int64) error {
	requesterID, err := taskOwner(actor)
	if err != nil {
		return err
	}

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

func (uc *TaskUseCase) ToggleStatus(ctx context.Context, actor domain.Claims, id int64) (domain.Task, error) {
	requesterID, err := taskOwner(actor)
	if err != nil {
		return domain.Task{}, err
	}

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

// taskOwner is the whole ownership rule: a task belongs to the account that
// created it. The bootstrap admin has no row in users and so can own none.
func taskOwner(actor domain.Claims) (int64, error) {
	switch {
	case actor.UserID > 0:
		return actor.UserID, nil
	case actor.UserID == 0 && actor.IsAdmin:
		return 0, apperr.ErrForbidden
	default:
		return 0, apperr.ErrUnauthorized
	}
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
