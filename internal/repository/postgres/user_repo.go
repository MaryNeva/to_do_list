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

const userColumns = "id, username, email, password_hash, created_at, updated_at"

type userModel struct {
	ID           int64     `db:"id"`
	Username     string    `db:"username"`
	Email        string    `db:"email"`
	PasswordHash string    `db:"password_hash"`
	CreatedAt    time.Time `db:"created_at"`
	UpdatedAt    time.Time `db:"updated_at"`
}

func (m userModel) toDomain() domain.User {
	return domain.User{
		ID:           m.ID,
		Username:     m.Username,
		Email:        m.Email,
		PasswordHash: m.PasswordHash,
		CreatedAt:    m.CreatedAt,
		UpdatedAt:    m.UpdatedAt,
	}
}

type UserRepository struct {
	pool *pgxpool.Pool
}

func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

var _ domain.UserRepository = (*UserRepository)(nil)

func (r *UserRepository) Create(ctx context.Context, user domain.User) (domain.User, error) {
	query := `INSERT INTO users (username, email, password_hash)
	          VALUES ($1, $2, $3)
	          RETURNING ` + userColumns

	rows, err := r.pool.Query(ctx, query, user.Username, user.Email, user.PasswordHash)
	if err != nil {
		if isUniqueViolation(err) {
			return domain.User{}, apperr.ErrConflict
		}
		return domain.User{}, fmt.Errorf("postgres: insert user: %w", err)
	}
	defer rows.Close()

	model, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[userModel])
	if err != nil {
		return domain.User{}, fmt.Errorf("postgres: scan created user: %w", err)
	}

	return model.toDomain(), nil
}

func (r *UserRepository) GetByID(ctx context.Context, id int64) (domain.User, error) {
	query := `SELECT ` + userColumns + ` FROM users WHERE id = $1`

	rows, err := r.pool.Query(ctx, query, id)
	if err != nil {
		return domain.User{}, fmt.Errorf("postgres: select user: %w", err)
	}
	defer rows.Close()

	model, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[userModel])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.User{}, apperr.ErrNotFound
		}
		return domain.User{}, fmt.Errorf("postgres: scan user: %w", err)
	}

	return model.toDomain(), nil
}

func (r *UserRepository) GetByUsername(ctx context.Context, username string) (domain.User, error) {
	query := `SELECT ` + userColumns + ` FROM users WHERE username = $1`

	rows, err := r.pool.Query(ctx, query, username)
	if err != nil {
		return domain.User{}, fmt.Errorf("postgres: select user by username: %w", err)
	}
	defer rows.Close()

	model, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[userModel])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.User{}, apperr.ErrNotFound
		}
		return domain.User{}, fmt.Errorf("postgres: scan user by username: %w", err)
	}

	return model.toDomain(), nil
}

func (r *UserRepository) List(ctx context.Context) ([]domain.User, error) {
	query := `SELECT ` + userColumns + ` FROM users ORDER BY id`

	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("postgres: select users: %w", err)
	}
	defer rows.Close()

	models, err := pgx.CollectRows(rows, pgx.RowToStructByName[userModel])
	if err != nil {
		return nil, fmt.Errorf("postgres: scan users: %w", err)
	}

	result := make([]domain.User, 0, len(models))
	for _, m := range models {
		result = append(result, m.toDomain())
	}

	return result, nil
}

func (r *UserRepository) Update(ctx context.Context, user domain.User) error {
	query := `UPDATE users SET username = $1, email = $2, password_hash = $3 WHERE id = $4`

	tag, err := r.pool.Exec(ctx, query, user.Username, user.Email, user.PasswordHash, user.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return apperr.ErrConflict
		}
		return fmt.Errorf("postgres: update user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.ErrNotFound
	}

	return nil
}

func (r *UserRepository) Delete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("postgres: delete user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.ErrNotFound
	}
	return nil
}
