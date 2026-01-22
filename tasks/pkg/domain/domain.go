package domain

import (
	"context"
	"time"
)

const StatusCreated = "created"
const StatusInProgress = "in_progress"
const StatusCompleted = "completed"

type Task struct {
	Id          int
	Title       string
	Description string
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Creator     int
}

type TaskUC interface {
	GetTask(ctx context.Context, id, creator int) (Task, error)
	GetListTasks(ctx context.Context, creator int) ([]Task, error)
	CreateTask(ctx context.Context, task Task) (Task, error)
	UpdateTask(ctx context.Context, id, creator int, task Task) (Task, error)
	DeleteTask(ctx context.Context, id, creator int) error
	ToggleStatus(ctx context.Context, id, creator int) (Task, error)
}

type TaskStore interface {
	ReadTaskById(ctx context.Context, id, creator int) (Task, error)
	ReadAllTasks(ctx context.Context, creator int) ([]Task, error)
	UpdateTask(ctx context.Context, id, creator int, task Task) error
	DeleteTask(ctx context.Context, id, creator int) error
	CreateTask(ctx context.Context, task Task) (Task, error)
	ToggleStatus(ctx context.Context, id, creator int, status string) error
}
