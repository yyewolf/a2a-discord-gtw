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
	got, err := s.Partials(ctx, "t", "req")
	if err != nil || len(got) != 2 {
		t.Fatalf("got %v, %v", got, err)
	}
	var first []string
	_ = json.Unmarshal(got[0], &first)
	if len(first) != 2 || first[1] != "Green" {
		t.Fatalf("first = %v", first)
	}
	if err := s.ClearPartials(ctx, "t"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Partials(ctx, "t", "req"); len(got) != 0 {
		t.Fatalf("after clear = %v", got)
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
