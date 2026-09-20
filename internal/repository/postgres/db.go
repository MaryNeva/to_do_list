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

// PoolConfig bounds what the pool may hold. Without it pgx defaults to four
// connections per core and keeps them forever, which is both unrelated to
// what Postgres can serve and blind to a database that has since restarted.
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
	// Dialling has its own budget: a connection the pool is still opening
	// must not hold a request past the caller's timeout.
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
