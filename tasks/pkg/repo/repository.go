package repo

import (
	"time"

	"to-do-list.com/tasks/pkg/domain"
)

type TaskModel struct {
	Id          int       `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Completed   bool      `json:"completed"`
	CreateAt    time.Time `json:"create_at"`
	UpdateAt    time.Time `json:"update_at"`
}

func toModelTask(entity domain.Task) TaskModel {
	return TaskModel{
		Id:          entity.Id,
		Title:       entity.Title,
		Description: entity.Description,
		Completed:   entity.Completed,
		CreateAt:    entity.CreateAt,
		UpdateAt:    entity.UpdateAt,
	}
}

func toTaskDomain(model TaskModel) domain.Task {
	return domain.Task{
		Id:          model.Id,
		Title:       model.Title,
		Description: model.Description,
		Completed:   model.Completed,
		CreateAt:    model.CreateAt,
		UpdateAt:    model.UpdateAt,
	}
}
