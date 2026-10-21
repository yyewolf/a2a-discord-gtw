package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/yyewolf/a2a-discord-gtw/internal/store"
)

func TestTasks(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if _, ok, err := s.Task(ctx, "t1"); ok || err != nil {
		t.Fatalf("ok = %v, err = %v", ok, err)
	}
	r := store.TaskRecord{TaskID: "t1", Agent: "a", ContextID: "c", State: "TASK_STATE_WORKING"}
	if err := s.PutTask(ctx, r); err != nil {
		t.Fatal(err)
	}
	r.State, r.PostedLen, r.RequestKey = "TASK_STATE_INPUT_REQUIRED", 42, "req-1"
	if err := s.PutTask(ctx, r); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Task(ctx, "t1")
	if !ok || err != nil || got.State != "TASK_STATE_INPUT_REQUIRED" || got.PostedLen != 42 || got.RequestKey != "req-1" || got.UpdatedAt.IsZero() {
		t.Fatalf("got %+v, %v, %v", got, ok, err)
	}
}

func TestUnsettledTasks(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	for id, state := range map[string]string{
		"working": "TASK_STATE_WORKING", "waiting": "TASK_STATE_INPUT_REQUIRED",
		"done": "TASK_STATE_COMPLETED", "failed": "TASK_STATE_FAILED", "canceled": "TASK_STATE_CANCELED",
	} {
		if err := s.PutTask(ctx, store.TaskRecord{TaskID: id, Agent: "a", ContextID: "c", State: state}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.UnsettledTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, r := range got {
		ids[r.TaskID] = true
	}
	if len(ids) != 2 || !ids["working"] || !ids["waiting"] {
		t.Fatalf("unsettled = %v", ids)
	}
}

func TestPruneTasks(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	for _, r := range []store.TaskRecord{
		{TaskID: "done", Agent: "a", ContextID: "c", State: "TASK_STATE_COMPLETED"},
		{TaskID: "waiting", Agent: "a", ContextID: "c", State: "TASK_STATE_INPUT_REQUIRED"},
	} {
		if err := s.PutTask(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.SetPartial(ctx, "done", "k", 0, "x")
	if n, err := s.PruneTasks(ctx, time.Now().Add(-time.Hour)); err != nil || n != 0 {
		t.Fatalf("pruned %d recent tasks, %v", n, err)
	}
	if n, err := s.PruneTasks(ctx, time.Now().Add(time.Hour)); err != nil || n != 1 {
		t.Fatalf("pruned %d, %v", n, err)
	}
	if _, ok, _ := s.Task(ctx, "done"); ok {
		t.Fatal("settled task kept")
	}
	if _, ok, _ := s.Task(ctx, "waiting"); !ok {
		t.Fatal("unsettled task pruned")
	}
	if _, ok, _ := s.TakePartials(ctx, "done", "k", 1); ok {
		t.Fatal("partials of a pruned task kept")
	}
}
