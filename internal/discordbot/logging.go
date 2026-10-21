package discordbot

import (
	"context"
	"log/slog"
)

// atLeast is a slog handler dropping records below min. DisGo logs every
// gateway payload at debug level, the Identify and Resume ones with the bot
// token in them, and REST URLs carrying interaction tokens; none of that may
// reach the logs, whatever the application's level.
type atLeast struct {
	slog.Handler
	min slog.Level
}

func (h atLeast) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= h.min && h.Handler.Enabled(ctx, l)
}

func (h atLeast) Handle(ctx context.Context, r slog.Record) error {
	if r.Level < h.min {
		return nil
	}
	return h.Handler.Handle(ctx, r)
}

func (h atLeast) WithAttrs(attrs []slog.Attr) slog.Handler {
	return atLeast{Handler: h.Handler.WithAttrs(attrs), min: h.min}
}

func (h atLeast) WithGroup(name string) slog.Handler {
	return atLeast{Handler: h.Handler.WithGroup(name), min: h.min}
}

// disgoLogger is the logger handed to DisGo: the application's, at info or
// above only.
func disgoLogger(log *slog.Logger) *slog.Logger {
	return slog.New(atLeast{Handler: log.Handler(), min: slog.LevelInfo}).With("component", "disgo")
}
