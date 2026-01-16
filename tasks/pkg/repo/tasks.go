package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"to-do-list.com/tasks/pkg/domain"
)

var tableTasks = "tasks"

type tasksStorePostgres struct {
	db *pgxpool.Pool
}

func (t *tasksStorePostgres) ReadTaskById(ctx context.Context, taskId int) (task domain.Task, err error) {
	query := fmt.Sprintf("SELECT * FROM %s WHERE id = $1;", tableTasks)

	rows, err := t.db.Query(ctx, query, taskId)
	if err != nil {
		return task, err
	}

	defer rows.Close()

	model, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[TaskModel])
	if errors.Is(err, pgx.ErrNoRows) {
		return task, err
	}

	if err != nil {
		return task, err
	}

	return toTaskDomain(model), err
}

func (t *tasksStorePostgres) ReadAllTasks(ctx context.Context) (tasks []domain.Task, err error) {
	rows, err := t.db.Query(ctx, tableTasks)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var models []domain.Task

	tasksModels, err := pgx.CollectRows(rows, pgx.RowToStructByName[TaskModel])
	if errors.Is(err, pgx.ErrNoRows) {
		return models, nil
	}
	if err != nil {
		return nil, err
	}
	for _, model := range tasksModels {
		tasks = append(tasks, toTaskDomain(model))
	}

	return tasks, nil
}

func (t *tasksStorePostgres) CreateTask(ctx context.Context, task domain.Task) (domain.Task, error) {
	model := toModelTask(task)

	query := fmt.Sprintf("INSERT INTO %s (title, description) VALUES ($1) RETURNING id", tableTasks)

	err := t.db.QueryRow(ctx, query, model.Title, model.Description).Scan(&model.Id)
	if err != nil {
		return domain.Task{}, err
	}

	return toTaskDomain(model), nil
}

func (t *tasksStorePostgres) UpdateTask(ctx context.Context, task domain.Task) error {
	model := toModelTask(task)

	query := fmt.Sprintf("UPDATE %s (title, description) VALUES ($1, $2)", tableTasks)
	_, err := t.db.Exec(ctx, query, model.Title, model.Description)
	if err != nil {
		return err
	}
	return nil
}

func (t *tasksStorePostgres) DeleteTask(ctx context.Context, taskId int) error {
	query := fmt.Sprintf("DELETE FROM %s WHERE id = $1;", tableTasks)
	_, err := t.db.Exec(ctx, query, taskId)
	return err
}

func NewTaskStore(dbPostgres *pgxpool.Pool) domain.TaskStore {
	return &tasksStorePostgres{db: dbPostgres}
}
