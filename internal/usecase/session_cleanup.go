package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"to-do-list/internal/domain"
)

type SessionCleanupConfig struct {
	Interval  time.Duration
	Retention time.Duration
	Timeout   time.Duration
}

type SessionCleaner struct {
	refresh domain.RefreshTokenRepository
	cfg     SessionCleanupConfig
	logger  *slog.Logger
	metrics MetricsRecorder
}

func NewSessionCleaner(refresh domain.RefreshTokenRepository, cfg SessionCleanupConfig, logger *slog.Logger, opts ...Option) *SessionCleaner {
	resolved := applyOptions(opts)
	return &SessionCleaner{refresh: refresh, cfg: cfg, logger: logger, metrics: resolved.metrics}
}

func (c *SessionCleaner) Run(ctx context.Context) {
	ticker := time.NewTicker(c.cfg.Interval)
	defer ticker.Stop()

	c.logger.Info("refresh token cleanup started", "interval", c.cfg.Interval, "retention", c.cfg.Retention)

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("refresh token cleanup stopped")
			return
		case <-ticker.C:
			if removed, err := c.CleanupOnce(ctx); err != nil {
				c.logger.ErrorContext(ctx, "refresh token cleanup failed", "error", err)
			} else if removed > 0 {
				c.logger.Info("expired refresh tokens removed", "count", removed)
			}
		}
	}
}

func (c *SessionCleaner) CleanupOnce(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	start := time.Now()
	cutoff := start.Add(-c.cfg.Retention)

	removed, err := c.refresh.DeleteExpired(ctx, cutoff)
	if err != nil {
		c.metrics.CleanupRun(OutcomeFailure, 0, time.Since(start))
		return 0, fmt.Errorf("delete expired refresh tokens: %w", err)
	}

	c.metrics.CleanupRun(OutcomeSuccess, removed, time.Since(start))
	return removed, nil
}
