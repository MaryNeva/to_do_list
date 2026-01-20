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
}

type List struct {
	List []Task `json:"list"`
}

func ToListPayload(list []domain.Task) List {
	var result List
	for _, user := range list {
		userP := ToTaskPayload(user)
		result.List = append(result.List, userP)
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
	}
}
