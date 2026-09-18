package usecase

import (
	"errors"
	"time"

	"to-do-list/internal/apperr"
)

const (
	OperationRegister = "register"
	OperationLogin    = "login"

	OutcomeSuccess  = "success"
	OutcomeRejected = "rejected"
	OutcomeFailure  = "failure"
	OutcomeReuse    = "reuse"
	OutcomeExpired  = "expired"
	OutcomeUnknown  = "unknown"

	ReasonLogout         = "logout"
	ReasonTokenReuse     = "token_reuse"
	ReasonPasswordChange = "password_change"
)

type MetricsRecorder interface {
	AuthAttempt(operation, outcome string)
	RefreshRotation(outcome string)
	SessionsRevoked(reason string, count int64)
	CleanupRun(outcome string, removed int64, d time.Duration)
}

type nopMetrics struct{}

func (nopMetrics) AuthAttempt(string, string)              {}
func (nopMetrics) RefreshRotation(string)                  {}
func (nopMetrics) SessionsRevoked(string, int64)           {}
func (nopMetrics) CleanupRun(string, int64, time.Duration) {}

type Option func(*options)

type options struct {
	metrics MetricsRecorder
}

func WithMetrics(recorder MetricsRecorder) Option {
	return func(o *options) {
		if recorder != nil {
			o.metrics = recorder
		}
	}
}

func applyOptions(opts []Option) options {
	resolved := options{metrics: nopMetrics{}}
	for _, opt := range opts {
		if opt != nil {
			opt(&resolved)
		}
	}
	return resolved
}

func outcomeOf(err error) string {
	switch {
	case err == nil:
		return OutcomeSuccess
	case errors.Is(err, apperr.ErrValidation),
		errors.Is(err, apperr.ErrConflict),
		errors.Is(err, apperr.ErrNotFound),
		errors.Is(err, apperr.ErrForbidden),
		errors.Is(err, apperr.ErrInvalidCredentials),
		errors.Is(err, apperr.ErrUnauthorized):
		return OutcomeRejected
	default:
		return OutcomeFailure
	}
}
