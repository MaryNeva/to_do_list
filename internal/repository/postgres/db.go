package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const uniqueViolationCode = "23505"

// PoolConfig overrides pgx defaults (connections per CPU, no lifetime limit)
// so the pool size is explicit and connections are recycled.
type PoolConfig struct {
	MaxConns       int32
	MinConns       int32
	MaxConnLife    time.Duration
	MaxConnIdle    time.Duration
	ConnectTimeout time.Duration
}

func NewPool(ctx context.Context, dsn string, poolCfg PoolConfig) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse dsn: %w", err)
	}

	cfg.MaxConns = poolCfg.MaxConns
	cfg.MinConns = poolCfg.MinConns
	cfg.MaxConnLifetime = poolCfg.MaxConnLife
	cfg.MaxConnIdleTime = poolCfg.MaxConnIdle
	// Bound dialling separately so a slow connect cannot outlast the caller.
	cfg.ConnConfig.ConnectTimeout = poolCfg.ConnectTimeout

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: create pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, poolCfg.ConnectTimeout)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}

	return pool, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode
}
