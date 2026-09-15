package domain

import (
	"context"
	"time"
)

type TaskStatus string

const (
	StatusCreated    TaskStatus = "created"
	StatusInProgress TaskStatus = "in_progress"
	StatusCompleted  TaskStatus = "completed"
)

func NextStatus(current TaskStatus) TaskStatus {
	switch current {
	case StatusCreated:
		return StatusInProgress
	case StatusInProgress:
		return StatusCompleted
	case StatusCompleted:
		return StatusCreated
	default:
		return StatusCreated
	}
}

// Task is the core to-do item entity.
type Task struct {
	ID          int64
	Title       string
	Description string
	Status      TaskStatus
	CreatorID   int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type TaskRepository interface {
	Create(ctx context.Context, task Task) (Task, error)
	GetByID(ctx context.Context, id int64) (Task, error)
	ListByCreator(ctx context.Context, creatorID int64) ([]Task, error)
	Update(ctx context.Context, task Task) (Task, error)
	Delete(ctx context.Context, id int64) error
	CompareAndSetStatus(ctx context.Context, id, ownerID int64, from, to TaskStatus) (Task, error)
}

type TaskService interface {
	Create(ctx context.Context, creatorID int64, title, description string) (Task, error)
	Get(ctx context.Context, requesterID, id int64) (Task, error)
	List(ctx context.Context, requesterID int64) ([]Task, error)
	Update(ctx context.Context, requesterID, id int64, title, description string) (Task, error)
	Delete(ctx context.Context, requesterID, id int64) error
	ToggleStatus(ctx context.Context, requesterID, id int64) (Task, error)
}
