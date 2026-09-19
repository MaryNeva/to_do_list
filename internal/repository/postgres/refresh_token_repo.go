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

const refreshTokenColumns = "id, user_id, token_hash, expires_at, revoked_at, created_at"

type refreshTokenModel struct {
	ID        int64      `db:"id"`
	UserID    int64      `db:"user_id"`
	TokenHash string     `db:"token_hash"`
	ExpiresAt time.Time  `db:"expires_at"`
	RevokedAt *time.Time `db:"revoked_at"`
	CreatedAt time.Time  `db:"created_at"`
}

func (m refreshTokenModel) toDomain() domain.RefreshToken {
	return domain.RefreshToken{
		ID:        m.ID,
		UserID:    m.UserID,
		TokenHash: m.TokenHash,
		ExpiresAt: m.ExpiresAt,
		RevokedAt: m.RevokedAt,
		CreatedAt: m.CreatedAt,
	}
}

type RefreshTokenRepository struct {
	pool *pgxpool.Pool
}

func NewRefreshTokenRepository(pool *pgxpool.Pool) *RefreshTokenRepository {
	return &RefreshTokenRepository{pool: pool}
}

func (r *RefreshTokenRepository) Create(ctx context.Context, token domain.RefreshToken, credentialsVersion int64) (domain.RefreshToken, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.RefreshToken{}, fmt.Errorf("postgres: begin create refresh token: %w", err)
	}
	defer tx.Rollback(ctx)

	var currentVersion int64
	err = tx.QueryRow(ctx,
		`SELECT credentials_version FROM users WHERE id = $1 FOR SHARE`, token.UserID).Scan(&currentVersion)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.RefreshToken{}, apperr.ErrNotFound
		}
		return domain.RefreshToken{}, fmt.Errorf("postgres: lock user for session: %w", err)
	}

	if currentVersion != credentialsVersion {
		return domain.RefreshToken{}, apperr.ErrConflict
	}

	query := `INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
	          VALUES ($1, $2, $3)
	          RETURNING ` + refreshTokenColumns

	rows, err := tx.Query(ctx, query, token.UserID, token.TokenHash, token.ExpiresAt)
	if err != nil {
		return domain.RefreshToken{}, fmt.Errorf("postgres: insert refresh token: %w", err)
	}
	model, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[refreshTokenModel])
	rows.Close()
	if err != nil {
		if isUniqueViolation(err) {
			return domain.RefreshToken{}, apperr.ErrConflict
		}
		return domain.RefreshToken{}, fmt.Errorf("postgres: scan created refresh token: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.RefreshToken{}, fmt.Errorf("postgres: commit create refresh token: %w", err)
	}

	return model.toDomain(), nil
}

func (r *RefreshTokenRepository) GetByHash(ctx context.Context, hash string) (domain.RefreshToken, error) {
	query := `SELECT ` + refreshTokenColumns + ` FROM refresh_tokens WHERE token_hash = $1`

	rows, err := r.pool.Query(ctx, query, hash)
	if err != nil {
		return domain.RefreshToken{}, fmt.Errorf("postgres: select refresh token: %w", err)
	}
	defer rows.Close()

	model, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[refreshTokenModel])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.RefreshToken{}, apperr.ErrNotFound
		}
		return domain.RefreshToken{}, fmt.Errorf("postgres: scan refresh token: %w", err)
	}

	return model.toDomain(), nil
}

func (r *RefreshTokenRepository) Revoke(ctx context.Context, hash string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now() WHERE token_hash = $1 AND revoked_at IS NULL`, hash)
	if err != nil {
		return fmt.Errorf("postgres: revoke refresh token: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.ErrNotFound
	}
	return nil
}

func (r *RefreshTokenRepository) RevokeAllForUser(ctx context.Context, userID int64) (int64, error) {
	var revoked int64

	err := r.inUserLock(ctx, userID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE refresh_tokens SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, userID)
		if err != nil {
			return fmt.Errorf("postgres: revoke refresh tokens of user: %w", err)
		}
		revoked = tag.RowsAffected()
		return nil
	})
	if err != nil {
		return 0, err
	}

	return revoked, nil
}

func (r *RefreshTokenRepository) inUserLock(ctx context.Context, userID int64, fn func(pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var locked int64
	if err := tx.QueryRow(ctx, `SELECT id FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apperr.ErrNotFound
		}
		return fmt.Errorf("postgres: lock user: %w", err)
	}

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit: %w", err)
	}
	return nil
}

func (r *RefreshTokenRepository) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM refresh_tokens WHERE expires_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("postgres: delete expired refresh tokens: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (r *RefreshTokenRepository) Rotate(
	ctx context.Context,
	presentedHash string,
	replacement domain.RefreshToken,
) (domain.RotateResult, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.RotateResult{}, fmt.Errorf("postgres: begin rotate: %w", err)
	}
	defer tx.Rollback(ctx)

	var ownerID int64
	err = tx.QueryRow(ctx, `SELECT user_id FROM refresh_tokens WHERE token_hash = $1`, presentedHash).Scan(&ownerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.RotateResult{}, apperr.ErrNotFound
		}
		return domain.RotateResult{}, fmt.Errorf("postgres: look up refresh token: %w", err)
	}

	var owner struct {
		id       int64
		username string
	}
	err = tx.QueryRow(ctx,
		`SELECT id, username FROM users WHERE id = $1 FOR UPDATE`, ownerID).Scan(&owner.id, &owner.username)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.RotateResult{}, apperr.ErrNotFound
		}
		return domain.RotateResult{}, fmt.Errorf("postgres: lock user for rotate: %w", err)
	}

	consumeRows, err := tx.Query(ctx, `UPDATE refresh_tokens
	          SET revoked_at = clock_timestamp()
	          WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > clock_timestamp()
	          RETURNING `+refreshTokenColumns, presentedHash)
	if err != nil {
		return domain.RotateResult{}, fmt.Errorf("postgres: consume refresh token: %w", err)
	}
	consumed, err := pgx.CollectExactlyOneRow(consumeRows, pgx.RowToStructByName[refreshTokenModel])
	consumeRows.Close()
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return domain.RotateResult{}, fmt.Errorf("postgres: scan consumed refresh token: %w", err)
		}
		return domain.RotateResult{UserID: ownerID, Username: owner.username}, r.classifyUnusable(ctx, tx, presentedHash)
	}

	issuedRows, err := tx.Query(ctx, `INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
	          VALUES ($1, $2, $3)
	          RETURNING `+refreshTokenColumns,
		consumed.UserID, replacement.TokenHash, replacement.ExpiresAt)
	if err != nil {
		return domain.RotateResult{}, fmt.Errorf("postgres: insert rotated refresh token: %w", err)
	}
	issued, err := pgx.CollectExactlyOneRow(issuedRows, pgx.RowToStructByName[refreshTokenModel])
	issuedRows.Close()
	if err != nil {
		if isUniqueViolation(err) {
			return domain.RotateResult{}, apperr.ErrConflict
		}
		return domain.RotateResult{}, fmt.Errorf("postgres: scan rotated refresh token: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.RotateResult{}, fmt.Errorf("postgres: commit rotate: %w", err)
	}

	return domain.RotateResult{
		Issued:   issued.toDomain(),
		UserID:   consumed.UserID,
		Username: owner.username,
	}, nil
}

func (r *RefreshTokenRepository) classifyUnusable(ctx context.Context, tx pgx.Tx, hash string) error {
	var revoked, expired bool
	err := tx.QueryRow(ctx,
		`SELECT revoked_at IS NOT NULL, expires_at <= clock_timestamp()
		 FROM refresh_tokens WHERE token_hash = $1`, hash).Scan(&revoked, &expired)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return apperr.ErrNotFound
	case err != nil:
		return fmt.Errorf("postgres: inspect refresh token: %w", err)
	case revoked:
		return apperr.ErrConflict
	case expired:
		return fmt.Errorf("%w: refresh token expired", apperr.ErrUnauthorized)
	default:
		return fmt.Errorf("%w: refresh token is not usable", apperr.ErrUnauthorized)
	}
}
