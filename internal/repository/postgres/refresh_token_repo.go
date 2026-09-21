package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"to-do-list/internal/apperr"
	"to-do-list/internal/domain"
)

const refreshTokenColumns = "id, user_id, token_hash, expires_at, revoked_at, created_at"

// Values of refresh_tokens.revoked_reason. Only a replayed reasonRotated token
// counts as reuse.
const (
	reasonRotated        = "rotated"
	reasonLogout         = "logout"
	reasonPasswordChange = "password_change"
	reasonReuse          = "reuse"
)

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
		// A hash collision is an internal failure, not a conflict the client caused.
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
		`UPDATE refresh_tokens SET revoked_at = now(), revoked_reason = $2
		 WHERE token_hash = $1 AND revoked_at IS NULL`, hash, reasonLogout)
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
		tag, err := revokeAllOfUser(ctx, tx, userID, reasonLogout)
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
	          SET revoked_at = clock_timestamp(), revoked_reason = $2
	          WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > clock_timestamp()
	          RETURNING `+refreshTokenColumns, presentedHash, reasonRotated)
	if err != nil {
		return domain.RotateResult{}, fmt.Errorf("postgres: consume refresh token: %w", err)
	}
	consumed, err := pgx.CollectExactlyOneRow(consumeRows, pgx.RowToStructByName[refreshTokenModel])
	consumeRows.Close()
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return domain.RotateResult{}, fmt.Errorf("postgres: scan consumed refresh token: %w", err)
		}
		// Still under the user lock: a replay revokes all sessions in this
		// transaction, or nothing is committed.
		revoked, reason := r.handleUnusable(ctx, tx, presentedHash, ownerID)
		if errors.Is(reason, apperr.ErrTokenReuse) {
			if err := tx.Commit(ctx); err != nil {
				return domain.RotateResult{}, fmt.Errorf("postgres: commit revocation after refresh token reuse: %w", err)
			}
		}
		return domain.RotateResult{
			UserID:          ownerID,
			Username:        owner.username,
			SessionsRevoked: revoked,
		}, reason
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
		// A hash collision is an internal failure, not a conflict the client caused.
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

// handleUnusable classifies a token that could not be consumed. Only a token
// that was already rotated is a replay: then all of the user's tokens are
// revoked in tx and ErrTokenReuse is returned (the caller commits). A token
// revoked by logout or a password change returns ErrTokenRevoked and writes nothing.
func (r *RefreshTokenRepository) handleUnusable(ctx context.Context, tx pgx.Tx, hash string, ownerID int64) (int64, error) {
	var (
		revoked, expired bool
		reason           *string
	)
	err := tx.QueryRow(ctx,
		`SELECT revoked_at IS NOT NULL, revoked_reason, expires_at <= clock_timestamp()
		 FROM refresh_tokens WHERE token_hash = $1`, hash).Scan(&revoked, &reason, &expired)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return 0, apperr.ErrNotFound
	case err != nil:
		return 0, fmt.Errorf("postgres: inspect refresh token: %w", err)
	case !revoked && expired:
		return 0, fmt.Errorf("%w: refresh token expired", apperr.ErrUnauthorized)
	case !revoked:
		return 0, fmt.Errorf("%w: refresh token is not usable", apperr.ErrUnauthorized)
	case reason == nil || *reason != reasonRotated:
		return 0, apperr.ErrTokenRevoked
	}

	tag, err := revokeAllOfUser(ctx, tx, ownerID, reasonReuse)
	if err != nil {
		return 0, fmt.Errorf("postgres: revoke sessions after refresh token reuse: %w", err)
	}

	return tag.RowsAffected(), apperr.ErrTokenReuse
}

// revokeAllOfUser revokes every active refresh token of userID with reason.
func revokeAllOfUser(ctx context.Context, tx pgx.Tx, userID int64, reason string) (pgconn.CommandTag, error) {
	return tx.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now(), revoked_reason = $2
		 WHERE user_id = $1 AND revoked_at IS NULL`, userID, reason)
}
