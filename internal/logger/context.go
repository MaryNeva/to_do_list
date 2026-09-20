package logger

import "context"

type contextKey struct{ name string }

var RequestIDKey = contextKey{"request_id"}

func (k contextKey) String() string { return "logger." + k.name }

func WithRequestID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, RequestIDKey, id)
}

func RequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(RequestIDKey).(string)
	return id
}
