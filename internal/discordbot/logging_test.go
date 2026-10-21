package discordbot

import (
	"log/slog"
	"strings"
	"testing"
)

func TestDisgoLoggerDropsDebug(t *testing.T) {
	var out strings.Builder
	app := slog.New(slog.NewTextHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug}))
	log := disgoLogger(app)
	log.Debug("sending gateway message", "data", `{"op":2,"d":{"token":"MTU2.secret"}}`)
	log.WithGroup("g").With("k", "v").Debug("also dropped")
	log.Info("connected")
	got := out.String()
	if strings.Contains(got, "secret") || strings.Contains(got, "also dropped") {
		t.Fatalf("debug records leaked: %q", got)
	}
	if !strings.Contains(got, "connected") || !strings.Contains(got, "component=disgo") {
		t.Fatalf("info record missing: %q", got)
	}
}
