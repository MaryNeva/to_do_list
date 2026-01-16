package domain

import (
	"context"
	"time"
)

const StatusCreated = "created"
const StatusInProgress = "in_progress"
const StatusCompleted = "completed"

type Task struct {
	Id          int       `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Completed   bool      `json:"completed"`
	CreateAt    time.Time `json:"create_at"`
	UpdateAt    time.Time `json:"update_at"`
}

type TaskUC interface {
	GetTask(ctx context.Context, id int) (Task, error)
	GetListTasks(ctx context.Context) ([]Task, error)
	CreateTask(ctx context.Context, task Task) (Task, error)
	UpdateTask(ctx context.Context, task Task) (Task, error)
	DeleteTask(ctx context.Context, id int) error
	//ToggleStatus func(ctx context.Context, id int) error
}

type TaskStore interface {
	ReadTaskById(ctx context.Context, id int) (Task, error)
	ReadAllTasks(ctx context.Context) ([]Task, error)
	UpdateTask(ctx context.Context, task Task) error
	DeleteTask(ctx context.Context, id int) error
	CreateTask(ctx context.Context, task Task) (Task, error)
	//ToggleStatus(ctx context.Context, id int) error
}
