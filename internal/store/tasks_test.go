package store_test

import (
	"context"
	"testing"

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
