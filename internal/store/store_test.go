package store_test

import (
	"context"
	"os"
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

func TestOpenOddPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "we?ird#dir")
	path := filepath.Join(dir, "state.db")
	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.SetAgentChannel(context.Background(), "a", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database not at %s: %v", path, err)
	}
}
