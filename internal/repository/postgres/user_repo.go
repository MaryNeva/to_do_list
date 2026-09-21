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

const userColumns = "id, username, email, password_hash, credentials_version, created_at, updated_at"

const updateUserQuery = `UPDATE users
          SET username            = COALESCE($2, username),
              email               = COALESCE($3, email),
              password_hash       = COALESCE($4, password_hash),
              credentials_version = credentials_version + $5
          WHERE id = $1
          RETURNING ` + userColumns

func credentialsBump(fields domain.UserUpdate) int64 {
	if fields.PasswordHash != nil {
		return 1
	}
	return 0
}

type userModel struct {
	ID                 int64     `db:"id"`
	Username           string    `db:"username"`
	Email              string    `db:"email"`
	PasswordHash       string    `db:"password_hash"`
	CredentialsVersion int64     `db:"credentials_version"`
	CreatedAt          time.Time `db:"created_at"`
	UpdatedAt          time.Time `db:"updated_at"`
}

func (m userModel) toDomain() domain.User {
	return domain.User{
		ID:                 m.ID,
		Username:           m.Username,
		Email:              m.Email,
		PasswordHash:       m.PasswordHash,
		CredentialsVersion: m.CredentialsVersion,
		CreatedAt:          m.CreatedAt,
		UpdatedAt:          m.UpdatedAt,
	}
}

type UserRepository struct {
	pool *pgxpool.Pool
}

func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

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
		if isUniqueViolation(err) {
			return domain.User{}, apperr.ErrConflict
		}
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
	query := `SELECT ` + userColumns + ` FROM users WHERE lower(username) = lower($1)`

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

func (r *UserRepository) List(ctx context.Context, page domain.PageRequest) (domain.Page[domain.User], error) {
	var total int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&total); err != nil {
		return domain.Page[domain.User]{}, fmt.Errorf("postgres: count users: %w", err)
	}

	query := `SELECT ` + userColumns + ` FROM users ORDER BY id LIMIT $1 OFFSET $2`

	rows, err := r.pool.Query(ctx, query, page.Limit, page.Offset)
	if err != nil {
		return domain.Page[domain.User]{}, fmt.Errorf("postgres: select users: %w", err)
	}
	defer rows.Close()

	models, err := pgx.CollectRows(rows, pgx.RowToStructByName[userModel])
	if err != nil {
		return domain.Page[domain.User]{}, fmt.Errorf("postgres: scan users: %w", err)
	}

	result := make([]domain.User, 0, len(models))
	for _, m := range models {
		result = append(result, m.toDomain())
	}

	return domain.NewPage(result, total, page), nil
}

func (r *UserRepository) Update(ctx context.Context, id int64, fields domain.UserUpdate) (domain.User, error) {
	rows, err := r.pool.Query(ctx, updateUserQuery, id, fields.Username, fields.Email, fields.PasswordHash, credentialsBump(fields))
	if err != nil {
		if isUniqueViolation(err) {
			return domain.User{}, apperr.ErrConflict
		}
		return domain.User{}, fmt.Errorf("postgres: update user: %w", err)
	}
	defer rows.Close()

	model, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[userModel])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.User{}, apperr.ErrNotFound
		}
		if isUniqueViolation(err) {
			return domain.User{}, apperr.ErrConflict
		}
		return domain.User{}, fmt.Errorf("postgres: scan updated user: %w", err)
	}

	return model.toDomain(), nil
}

func (r *UserRepository) UpdateAndRevokeSessions(ctx context.Context, id int64, fields domain.UserUpdate) (domain.User, int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.User{}, 0, fmt.Errorf("postgres: begin update user: %w", err)
	}
	defer tx.Rollback(ctx)

	var locked int64
	if err := tx.QueryRow(ctx, `SELECT id FROM users WHERE id = $1 FOR UPDATE`, id).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.User{}, 0, apperr.ErrNotFound
		}
		return domain.User{}, 0, fmt.Errorf("postgres: lock user: %w", err)
	}

	rows, err := tx.Query(ctx, updateUserQuery, id, fields.Username, fields.Email, fields.PasswordHash, credentialsBump(fields))
	if err != nil {
		if isUniqueViolation(err) {
			return domain.User{}, 0, apperr.ErrConflict
		}
		return domain.User{}, 0, fmt.Errorf("postgres: update user: %w", err)
	}
	model, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[userModel])
	rows.Close()
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.User{}, 0, apperr.ErrNotFound
		}
		if isUniqueViolation(err) {
			return domain.User{}, 0, apperr.ErrConflict
		}
		return domain.User{}, 0, fmt.Errorf("postgres: scan updated user: %w", err)
	}

	tag, err := revokeAllOfUser(ctx, tx, id, reasonPasswordChange)
	if err != nil {
		return domain.User{}, 0, fmt.Errorf("postgres: revoke sessions of user: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.User{}, 0, fmt.Errorf("postgres: commit update user: %w", err)
	}

	return model.toDomain(), tag.RowsAffected(), nil
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
