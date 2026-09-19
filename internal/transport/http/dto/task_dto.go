package dto

import (
	"time"

	"to-do-list/internal/domain"
)

type CreateTaskRequest struct {
	Title       string `json:"title" validate:"required"`
	Description string `json:"description"`
}

// UpdateTaskRequest is a partial edit: an omitted field keeps its value, a
// field sent as "" clears it (title excepted), and null reads as omitted.
type UpdateTaskRequest struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Status      *string `json:"status"`
}

func (r UpdateTaskRequest) ToDomain() domain.TaskUpdate {
	update := domain.TaskUpdate{Title: r.Title, Description: r.Description}
	if r.Status != nil {
		status := domain.TaskStatus(*r.Status)
		update.Status = &status
	}
	return update
}

type TaskResponse struct {
	ID          int64     `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Status      string    `json:"status"`
	CreatorID   int64     `json:"creator_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func ToTaskResponse(t domain.Task) TaskResponse {
	return TaskResponse{
		ID:          t.ID,
		Title:       t.Title,
		Description: t.Description,
		Status:      string(t.Status),
		CreatorID:   t.CreatorID,
		CreatedAt:   t.CreatedAt,
		UpdatedAt:   t.UpdatedAt,
	}
}

func ToTaskListResponse(tasks []domain.Task) []TaskResponse {
	result := make([]TaskResponse, 0, len(tasks))
	for _, t := range tasks {
		result = append(result, ToTaskResponse(t))
	}
	return result
}
