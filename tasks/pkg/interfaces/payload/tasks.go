package payload

import (
	"time"

	"to-do-list.com/tasks/pkg/domain"
)

type Task struct {
	Id          int       `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Deadline    Deadline  `json:"deadline,omitempty"`
	Creator     int       `json:"creator"`
}

type Deadline struct {
	Message    string     `json:"message,omitempty"`
	DeadlineAt *time.Time `json:"deadline_at,omitempty"`
}

type List struct {
	List []Task `json:"list"`
}

func ToListPayload(list []domain.Task) List {
	var result List
	for _, task := range list {
		taskP := ToTaskPayload(task)
		result.List = append(result.List, taskP)
	}

	return result
}

func ToTaskDomain(task Task) domain.Task {
	return domain.Task{
		Id:          task.Id,
		Title:       task.Title,
		Description: task.Description,
		Status:      task.Status,
		CreatedAt:   task.CreatedAt,
		UpdatedAt:   task.UpdatedAt,
		Deadline: domain.Deadline{
			Message:    task.Deadline.Message,
			DeadlineAt: task.Deadline.DeadlineAt,
		},
		Creator: task.Creator,
	}
}

func ToTaskPayload(task domain.Task) Task {
	return Task{
		Id:          task.Id,
		Title:       task.Title,
		Description: task.Description,
		Status:      task.Status,
		CreatedAt:   task.CreatedAt,
		UpdatedAt:   task.UpdatedAt,
		Deadline: Deadline{
			Message:    task.Deadline.Message,
			DeadlineAt: task.Deadline.DeadlineAt,
		},
		Creator: task.Creator,
	}
}
