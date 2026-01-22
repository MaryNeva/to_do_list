package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"to-do-list.com/tasks/pkg/domain"
)

var (
	tableTasks = "tasks"
)

type tasksStorePostgres struct {
	db *pgxpool.Pool
}

func (t *tasksStorePostgres) ReadTaskById(ctx context.Context, taskId, creator int) (task domain.Task, err error) {
	query := fmt.Sprintf("SELECT * FROM %s WHERE id = $1 AND creator = $2;", tableTasks)

	rows, err := t.db.Query(ctx, query, taskId, creator)
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

func (t *tasksStorePostgres) ReadAllTasks(ctx context.Context, creator int) (tasks []domain.Task, err error) {
	query := fmt.Sprintf("SELECT * FROM %s WHERE creator = $1;", tableTasks)
	rows, err := t.db.Query(ctx, query, creator)

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

	query := fmt.Sprintf("INSERT INTO %s (title, description, creator) VALUES ($1, $2, $3) RETURNING id, status, creator", tableTasks)

	err := t.db.QueryRow(ctx, query, model.Title, model.Description, model.Creator).Scan(&model.Id, &model.Status, &model.Creator)
	if err != nil {
		return domain.Task{}, err
	}

	return toTaskDomain(model), nil
}

func (t *tasksStorePostgres) UpdateTask(ctx context.Context, id, creator int, task domain.Task) error {
	model := toModelTask(task)
	query := fmt.Sprintf("UPDATE %s SET title = $1, description = $2 WHERE id = $3 AND creator = $4;", tableTasks)

	_, err := t.db.Exec(ctx, query, model.Title, model.Description, id, creator)
	if err != nil {
		return err
	}

	return nil
}

func (t *tasksStorePostgres) DeleteTask(ctx context.Context, taskId, creator int) error {
	query := fmt.Sprintf("DELETE FROM %s WHERE id = $1 AND creator = $2;", tableTasks)

	cmd, err := t.db.Exec(ctx, query, taskId, creator)
	if err != nil {
		return err
	}

	if cmd.RowsAffected() == 0 {
		return errors.New("task not found or not owned by user")
	}

	return nil
}

func (t *tasksStorePostgres) ToggleStatus(ctx context.Context, id, creator int, status string) error {
	query := fmt.Sprintf("UPDATE %s SET status = $2 WHERE id = $1 AND creator = $3;", tableTasks)

	cmdTag, err := t.db.Exec(ctx, query, id, status, creator)
	if err != nil {
		return err
	}

	if cmdTag.RowsAffected() == 0 {
		return err
	}

	return nil
}

func NewTaskStore(db *pgxpool.Pool) domain.TaskStore { return &tasksStorePostgres{db: db} }
