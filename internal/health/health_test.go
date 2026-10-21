package health_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yyewolf/a2a-discord-gtw/internal/health"
)

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil))
	return rec
}

func TestHealthz(t *testing.T) {
	h := health.Handler(health.Check{Name: "x", Fn: func(context.Context) error { return errors.New("down") }})
	if rec := get(t, h, "/healthz"); rec.Code != http.StatusOK {
		t.Fatalf("healthz = %d", rec.Code)
	}
}

func TestReadyz(t *testing.T) {
	ok := health.Check{Name: "ok", Fn: func(context.Context) error { return nil }}
	down := health.Check{Name: "discord", Fn: func(context.Context) error { return errors.New("gateway closed") }}
	if rec := get(t, health.Handler(ok), "/readyz"); rec.Code != http.StatusOK {
		t.Fatalf("ready = %d", rec.Code)
	}
	rec := get(t, health.Handler(ok, down), "/readyz")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "discord: gateway closed") {
		t.Fatalf("not ready = %d %q", rec.Code, rec.Body.String())
	}
}
