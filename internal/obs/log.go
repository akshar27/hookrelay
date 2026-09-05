// Package obs wires observability: structured logging and Prometheus metrics.
package obs

import (
	"context"
	"log/slog"
	"os"
)

type ctxKey int

const loggerKey ctxKey = 0

// NewLogger returns a JSON slog logger in production and a text one otherwise.
func NewLogger(env string) *slog.Logger {
	level := slog.LevelInfo
	if env == "development" {
		return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}

// WithLogger stashes a logger (typically request-scoped, with a request id) on the context.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, l)
}

// LoggerFrom returns the context logger, or the default logger if none is set.
func LoggerFrom(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}
