package app

import (
	"context"
	"fmt"
	"time"

	tasks "to-do-list.com/tasks/pkg/domain"
)

var timeReminder = 24 * time.Hour

type taskStore struct {
	store    tasks.TaskStore
	duration time.Duration
}

func (t *taskStore) GetListTasks(ctx context.Context, creator int) ([]tasks.Task, error) {
	list, err := t.store.ReadAllTasks(ctx, creator)
	if err != nil {
		return nil, err
	}

	return list, nil
}

func (t *taskStore) GetTask(ctx context.Context, id, creator int) (tasks.Task, error) {
	task, err := t.store.ReadTaskById(ctx, id, creator)
	if err != nil {
		return tasks.Task{}, err
	}

	CheckDeadline(&task)

	return task, nil
}

func (t *taskStore) CreateTask(ctx context.Context, req tasks.Task) (tasks.Task, error) {
	task, err := t.store.CreateTask(ctx, req)
	if err != nil {
		return tasks.Task{}, err
	}

	result, err := t.store.ReadTaskById(ctx, task.Id, task.Creator)
	if err != nil {
		return tasks.Task{}, err
	}

	CheckDeadline(&task)

	return result, nil
}

func (t *taskStore) UpdateTask(ctx context.Context, id, creator int, task tasks.Task) (tasks.Task, error) {
	err := t.store.UpdateTask(ctx, id, creator, task)
	if err != nil {
		return tasks.Task{}, err
	}

	return t.store.ReadTaskById(ctx, id, creator)
}

func (t *taskStore) DeleteTask(ctx context.Context, id, creator int) error {
	return t.store.DeleteTask(ctx, id, creator)
}

func (t *taskStore) ToggleStatus(ctx context.Context, id, creator int) (tasks.Task, error) {
	task, err := t.store.ReadTaskById(ctx, id, creator)
	if err != nil {
		return tasks.Task{}, err
	}

	status := nextStatus(task.Status)
	err = t.store.ToggleStatus(ctx, id, creator, status)
	if err != nil {
		return tasks.Task{}, err
	}

	return t.store.ReadTaskById(ctx, id, creator)
}

func NewTaskControl(store tasks.TaskStore, duration time.Duration) tasks.TaskUC {
	return &taskStore{
		store:    store,
		duration: duration,
	}
}

func nextStatus(current string) string {
	switch current {
	case tasks.StatusCreated:
		return tasks.StatusInProgress
	case tasks.StatusInProgress:
		return tasks.StatusCompleted
	case tasks.StatusCompleted:
		return tasks.StatusCreated
	default:
		return tasks.StatusCreated
	}
}

func CheckDeadline(task *tasks.Task) {
	now := time.Now()
	if task.Deadline.DeadlineAt == nil {
		return
	}

	deadline := *task.Deadline.DeadlineAt

	switch {
	case now.After(deadline):
		task.Deadline.Message = tasks.MessageExpired
	case deadline.Sub(now) <= timeReminder:
		task.Deadline.Message = tasks.MessageReminder + FormatRemainingTime(deadline)
	default:
		task.Deadline.Message = ""
	}
}

func FormatRemainingTime(deadline time.Time) string {
	now := time.Now()
	if now.After(deadline) {
		return "Deadline expired"
	}

	remaining := deadline.Sub(now) // time.Duration

	hours := int(remaining.Hours())
	minutes := int(remaining.Minutes()) % 60
	seconds := int(remaining.Seconds()) % 60

	return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
}
