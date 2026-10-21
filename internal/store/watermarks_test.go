package store_test

import (
	"context"
	"testing"
	"time"
)

func TestWatermark(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if _, ok, err := s.Watermark(ctx, "a"); ok || err != nil {
		t.Fatalf("ok = %v, err = %v", ok, err)
	}
	at := time.Date(2026, 10, 21, 18, 57, 20, 29582701, time.UTC)
	if err := s.SetWatermark(ctx, "a", at); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Watermark(ctx, "a")
	if !ok || err != nil || !got.Equal(at) {
		t.Fatalf("got %s, %v, %v; want %s", got, ok, err, at)
	}
}
