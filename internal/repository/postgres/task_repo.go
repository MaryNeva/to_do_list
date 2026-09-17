package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"to-do-list/internal/apperr"
	"to-do-list/internal/domain"
)

const taskColumns = "id, title, description, status, creator_id, created_at, updated_at"

type taskModel struct {
	ID          int64     `db:"id"`
	Title       string    `db:"title"`
	Description string    `db:"description"`
	Status      string    `db:"status"`
	CreatorID   int64     `db:"creator_id"`
	CreatedAt   time.Time `db:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"`
}

func (m taskModel) toDomain() domain.Task {
	return domain.Task{
		ID:          m.ID,
		Title:       m.Title,
		Description: m.Description,
		Status:      domain.TaskStatus(m.Status),
		CreatorID:   m.CreatorID,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
}

type TaskRepository struct {
	pool *pgxpool.Pool
}

func NewTaskRepository(pool *pgxpool.Pool) *TaskRepository {
	return &TaskRepository{pool: pool}
}

var _ domain.TaskRepository = (*TaskRepository)(nil)

func (r *TaskRepository) Create(ctx context.Context, task domain.Task) (domain.Task, error) {
	query := `INSERT INTO tasks (title, description, status, creator_id)
	          VALUES ($1, $2, $3, $4)
	          RETURNING ` + taskColumns

	rows, err := r.pool.Query(ctx, query, task.Title, task.Description, string(task.Status), task.CreatorID)
	if err != nil {
		return domain.Task{}, fmt.Errorf("postgres: insert task: %w", err)
	}
	defer rows.Close()

	model, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[taskModel])
	if err != nil {
		return domain.Task{}, fmt.Errorf("postgres: scan created task: %w", err)
	}

	return model.toDomain(), nil
}

func (r *TaskRepository) GetByID(ctx context.Context, id int64) (domain.Task, error) {
	query := `SELECT ` + taskColumns + ` FROM tasks WHERE id = $1`

	rows, err := r.pool.Query(ctx, query, id)
	if err != nil {
		return domain.Task{}, fmt.Errorf("postgres: select task: %w", err)
	}
	defer rows.Close()

	model, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[taskModel])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Task{}, apperr.ErrNotFound
		}
		return domain.Task{}, fmt.Errorf("postgres: scan task: %w", err)
	}

	return model.toDomain(), nil
}

func (r *TaskRepository) ListByCreator(ctx context.Context, creatorID int64, filter domain.TaskFilter) (domain.Page[domain.Task], error) {
	args := []any{creatorID}
	where := "WHERE creator_id = $1"
	if filter.Status != nil {
		args = append(args, string(*filter.Status))
		where += " AND status = $2"
	}

	var total int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM tasks `+where, args...).Scan(&total); err != nil {
		return domain.Page[domain.Task]{}, fmt.Errorf("postgres: count tasks: %w", err)
	}

	query := `SELECT ` + taskColumns + ` FROM tasks ` + where +
		` ORDER BY ` + orderClause(filter) +
		fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
	args = append(args, filter.Page.Limit, filter.Page.Offset)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return domain.Page[domain.Task]{}, fmt.Errorf("postgres: select tasks: %w", err)
	}
	defer rows.Close()

	models, err := pgx.CollectRows(rows, pgx.RowToStructByName[taskModel])
	if err != nil {
		return domain.Page[domain.Task]{}, fmt.Errorf("postgres: scan tasks: %w", err)
	}

	result := make([]domain.Task, 0, len(models))
	for _, m := range models {
		result = append(result, m.toDomain())
	}

	return domain.NewPage(result, total, filter.Page), nil
}

func orderClause(filter domain.TaskFilter) string {
	column := "created_at"
	switch filter.Sort {
	case domain.SortByUpdatedAt:
		column = "updated_at"
	case domain.SortByTitle:
		column = "title"
	case domain.SortByStatus:
		column = "status"
	}

	direction := "DESC"
	if filter.Order == domain.OrderAsc {
		direction = "ASC"
	}

	return column + " " + direction + ", id " + direction
}

func (r *TaskRepository) Update(ctx context.Context, task domain.Task) (domain.Task, error) {
	query := `UPDATE tasks SET title = $1, description = $2 WHERE id = $3 RETURNING ` + taskColumns

	rows, err := r.pool.Query(ctx, query, task.Title, task.Description, task.ID)
	if err != nil {
		return domain.Task{}, fmt.Errorf("postgres: update task: %w", err)
	}
	defer rows.Close()

	model, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[taskModel])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Task{}, apperr.ErrNotFound
		}
		return domain.Task{}, fmt.Errorf("postgres: scan updated task: %w", err)
	}

	return model.toDomain(), nil
}

func (r *TaskRepository) Delete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM tasks WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("postgres: delete task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.ErrNotFound
	}
	return nil
}

func (r *TaskRepository) CompareAndSetStatus(ctx context.Context, id, ownerID int64, from, to domain.TaskStatus) (domain.Task, error) {
	query := `UPDATE tasks SET status = $4
	          WHERE id = $1 AND creator_id = $2 AND status = $3
	          RETURNING ` + taskColumns

	rows, err := r.pool.Query(ctx, query, id, ownerID, string(from), string(to))
	if err != nil {
		return domain.Task{}, fmt.Errorf("postgres: compare-and-set task status: %w", err)
	}
	defer rows.Close()

	model, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[taskModel])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Task{}, r.classifyMissedTransition(ctx, id, ownerID)
		}
		return domain.Task{}, fmt.Errorf("postgres: scan task after status change: %w", err)
	}

	return model.toDomain(), nil
}

func (r *TaskRepository) classifyMissedTransition(ctx context.Context, id, ownerID int64) error {
	var creatorID int64
	err := r.pool.QueryRow(ctx, `SELECT creator_id FROM tasks WHERE id = $1`, id).Scan(&creatorID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return apperr.ErrNotFound
	case err != nil:
		return fmt.Errorf("postgres: inspect task after failed status change: %w", err)
	case creatorID != ownerID:
		return apperr.ErrNotFound
	default:
		return apperr.ErrConflict
	}
}
