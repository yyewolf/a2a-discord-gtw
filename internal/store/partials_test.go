package store_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
)

func TestPartials(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if err := s.SetPartial(ctx, "t", "req", 1, []string{"Blue"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPartial(ctx, "t", "req", 0, []string{"x"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPartial(ctx, "t", "req", 0, []string{"Red", "Green"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPartial(ctx, "t", "other", 0, []string{"stale"}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.TakePartials(ctx, "t", "req", 2)
	if err != nil || !ok || len(got) != 2 {
		t.Fatalf("got %v, %v, %v", got, ok, err)
	}
	var first []string
	_ = json.Unmarshal(got[0], &first)
	if len(first) != 2 || first[1] != "Green" {
		t.Fatalf("first = %v", first)
	}
	_ = s.SetPartial(ctx, "u", "req", 0, "x")
	if err := s.ClearPartials(ctx, "u"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.TakePartials(ctx, "u", "req", 1); ok {
		t.Fatal("cleared partials still there")
	}
}

func TestTakePartials(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	_ = s.SetPartial(ctx, "t", "req", 0, []string{"a"})
	if _, ok, err := s.TakePartials(ctx, "t", "req", 2); ok || err != nil {
		t.Fatalf("took an incomplete set: %v, %v", ok, err)
	}
	_ = s.SetPartial(ctx, "t", "req", 1, []string{"b"})
	var wg sync.WaitGroup
	var mu sync.Mutex
	taken := 0
	for range 8 {
		wg.Go(func() {
			if _, ok, err := s.TakePartials(ctx, "t", "req", 2); err != nil {
				t.Error(err)
			} else if ok {
				mu.Lock()
				taken++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if taken != 1 {
		t.Fatalf("the complete set was taken %d times", taken)
	}
}
