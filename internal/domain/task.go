package domain

import (
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

type TaskSortField string

const (
	SortByCreatedAt TaskSortField = "created_at"
	SortByUpdatedAt TaskSortField = "updated_at"
	SortByTitle     TaskSortField = "title"
	SortByStatus    TaskSortField = "status"
)

type SortOrder string

const (
	OrderAsc  SortOrder = "asc"
	OrderDesc SortOrder = "desc"
)

func IsValidStatus(status TaskStatus) bool {
	switch status {
	case StatusCreated, StatusInProgress, StatusCompleted:
		return true
	default:
		return false
	}
}

type TaskFilter struct {
	Status *TaskStatus
	Sort   TaskSortField
	Order  SortOrder
	Page   PageRequest
}

// TaskUpdate is a partial edit: nil fields are unchanged, non-nil fields are
// written (an empty Description clears it).
type TaskUpdate struct {
	Title       *string
	Description *string
	Status      *TaskStatus
}

func (u TaskUpdate) IsEmpty() bool {
	return u.Title == nil && u.Description == nil && u.Status == nil
}

type Task struct {
	ID          int64
	Title       string
	Description string
	Status      TaskStatus
	CreatorID   int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
