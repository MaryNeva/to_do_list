package repo

import (
	"time"

	"to-do-list.com/tasks/pkg/domain"
)

type TaskModel struct {
	Id          int       `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func toModelTask(entity domain.Task) TaskModel {
	return TaskModel{
		Id:          entity.Id,
		Title:       entity.Title,
		Description: entity.Description,
		Status:      entity.Status,
		CreatedAt:   entity.CreatedAt,
		UpdatedAt:   entity.UpdatedAt,
	}
}

func toTaskDomain(model TaskModel) domain.Task {
	return domain.Task{
		Id:          model.Id,
		Title:       model.Title,
		Description: model.Description,
		Status:      model.Status,
		CreatedAt:   model.CreatedAt,
		UpdatedAt:   model.UpdatedAt,
	}
}
