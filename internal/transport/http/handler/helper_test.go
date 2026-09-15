package handler

import (
	"io"
	"log/slog"
)

func silentTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
