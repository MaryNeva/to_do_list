package app

import (
	"context"
	"time"

	tasks "to-do-list.com/tasks/pkg/domain"
)

type taskStore struct {
	store    tasks.TaskStore
	duration time.Duration
}

func (t *taskStore) GetListTasks(ctx context.Context) ([]tasks.Task, error) {
	list, err := t.store.ReadAllTasks(ctx)
	if err != nil {
		return nil, err
	}

	return list, nil
}

func (t *taskStore) GetTask(ctx context.Context, id int) (tasks.Task, error) {
	task, err := t.store.ReadTaskById(ctx, id)
	if err != nil {
		return tasks.Task{}, err
	}

	return task, nil
}

func (t *taskStore) CreateTask(ctx context.Context, task tasks.Task) (tasks.Task, error) {
	task, err := t.store.CreateTask(ctx, task)
	if err != nil {
		return tasks.Task{}, err
	}

	return task, nil
}

func (t *taskStore) UpdateTask(ctx context.Context, id int, task tasks.Task) (tasks.Task, error) {
	err := t.store.UpdateTask(ctx, id, task)
	if err != nil {
		return tasks.Task{}, err
	}

	return task, nil
}

func (t *taskStore) DeleteTask(ctx context.Context, id int) error {
	return t.store.DeleteTask(ctx, id)
}

func (t *taskStore) ToggleStatus(ctx context.Context, id int) (tasks.Task, error) {
	task, err := t.store.ReadTaskById(ctx, id)
	if err != nil {
		return tasks.Task{}, err
	}

	status := nextStatus(task.Status)
	err = t.store.ToggleStatus(ctx, id, status)
	if err != nil {
		return tasks.Task{}, err
	}

	return t.store.ReadTaskById(ctx, id)
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
