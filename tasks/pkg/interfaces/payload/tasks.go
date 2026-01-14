package payload

import (
	"time"

	"to-do-list.com/tasks/pkg/domain"
)

type Task struct {
	Id          int       `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Completed   bool      `json:"completed"`
	CreateAt    time.Time `json:"create_at"`
	UpdateAt    time.Time `json:"update_at"`
}

func ToTaskDomain(task Task) domain.Task {
	return domain.Task{
		Id:          task.Id,
		Title:       task.Title,
		Description: task.Description,
		Completed:   task.Completed,
		CreateAt:    task.CreateAt,
		UpdateAt:    task.UpdateAt,
	}
}

func ToTaskPayload(task domain.Task) Task {
	return Task{
		Id:          task.Id,
		Title:       task.Title,
		Description: task.Description,
		Completed:   task.Completed,
		CreateAt:    task.CreateAt,
		UpdateAt:    task.UpdateAt,
	}
}
