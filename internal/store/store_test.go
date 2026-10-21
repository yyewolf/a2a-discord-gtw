package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/yyewolf/a2a-discord-gtw/internal/store"
)

func open(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "sub", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	for range 2 {
		s, err := store.Open(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Ping(context.Background()); err != nil {
			t.Fatal(err)
		}
		_ = s.Close()
	}
}
