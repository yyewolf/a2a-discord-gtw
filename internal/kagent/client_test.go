package kagent_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/yyewolf/a2a-discord-gtw/internal/kagent"
	"github.com/yyewolf/a2a-discord-gtw/internal/kagent/kagenttest"
)

func newAgent(t *testing.T, srv *kagenttest.Server) *kagent.Agent {
	t.Helper()
	a, err := kagent.NewAgent(context.Background(), srv.URL+"/agents/kagent/fake", kagent.Options{UserID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func collect(t *testing.T, a *kagent.Agent, m *a2a.Message) ([]a2a.Event, error) {
	t.Helper()
	var evs []a2a.Event
	for ev, err := range a.Stream(context.Background(), m) {
		if err != nil {
			return evs, err
		}
		evs = append(evs, ev)
	}
	return evs, nil
}

// TestQuestionRoundTrip replays what kagent did live: a question pauses the
// task with the request only in metadata, plain text on the context is
// refused, and the structured answer resumes and completes the same task.
func TestQuestionRoundTrip(t *testing.T) {
	srv := kagenttest.New()
	defer srv.Close()
	paused := decodeTask(t, askUserTask)
	srv.OnSend(func(m *a2a.Message) ([]a2a.Event, *kagenttest.RPCError) {
		switch {
		case m.TaskID == "" && m.ContextID == "":
			return []a2a.Event{paused}, nil
		case m.TaskID == "":
			return nil, kagenttest.WaitingError()
		default:
			done := *paused
			done.Status = a2a.TaskStatus{State: a2a.TaskStateCompleted}
			done.Artifacts = []*a2a.Artifact{artifact("a", ""), artifact("b", " YOU PICKED Blue.")}
			return []a2a.Event{
				&a2a.TaskArtifactUpdateEvent{TaskID: done.ID, ContextID: done.ContextID, Artifact: artifact("b", " YOU PICKED Blue.")},
				&done,
			}, nil
		}
	})
	a := newAgent(t, srv)

	evs, err := collect(t, a, kagent.TextMessage("", "ask me a colour"))
	if err != nil {
		t.Fatal(err)
	}
	task, ok := evs[len(evs)-1].(*a2a.Task)
	if !ok {
		t.Fatalf("last event = %T", evs[len(evs)-1])
	}
	req, err := kagent.PendingRequest(task)
	if err != nil {
		t.Fatal(err)
	}

	_, err = collect(t, a, kagent.TextMessage(task.ContextID, "blue"))
	if !kagent.IsWaitingForInput(err) {
		t.Fatalf("plain reply err = %v, want waiting-for-input", err)
	}

	answer, err := kagent.AnswerMessage(task, req, [][]string{{"Blue"}}, "Blue")
	if err != nil {
		t.Fatal(err)
	}
	evs, err = collect(t, a, answer)
	if err != nil {
		t.Fatal(err)
	}
	final := evs[len(evs)-1].(*a2a.Task)
	if final.Status.State != a2a.TaskStateCompleted || kagent.TaskText(final) != "YOU PICKED Blue." {
		t.Fatalf("final = %s %q", final.Status.State, kagent.TaskText(final))
	}

	call, _ := srv.LastCall("SendStreamingMessage")
	if got := call.Header.Get("A2A-Version"); got != "1.0" {
		t.Errorf("A2A-Version = %q", got)
	}
	if got := call.Header.Get("A2A-Extensions"); got != kagent.HITLExtension {
		t.Errorf("A2A-Extensions = %q", got)
	}
	if got := call.Header.Get("X-User-Id"); got != "owner" {
		t.Errorf("X-User-Id = %q", got)
	}
	var p struct {
		Message json.RawMessage `json:"message"`
	}
	_ = json.Unmarshal(call.Params, &p)
	if !strings.Contains(string(p.Message), `"answers":[{"answer":["Blue"]}]`) {
		t.Errorf("answer on the wire = %s", p.Message)
	}
}

func TestTasksSinceAndGet(t *testing.T) {
	srv := kagenttest.New()
	defer srv.Close()
	old := time.Now().Add(-time.Hour)
	recent := time.Now()
	srv.PutTask(&a2a.Task{ID: "old", ContextID: "c", Status: a2a.TaskStatus{State: a2a.TaskStateCompleted, Timestamp: &old}})
	srv.PutTask(&a2a.Task{ID: "new", ContextID: "c", Status: a2a.TaskStatus{State: a2a.TaskStateWorking, Timestamp: &recent}})
	a := newAgent(t, srv)

	tasks, err := a.TasksSince(context.Background(), time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != "new" {
		t.Fatalf("tasks = %v", tasks)
	}
	got, err := a.Task(context.Background(), "new")
	if err != nil || got.Status.State != a2a.TaskStateWorking {
		t.Fatalf("task = %v, err = %v", got, err)
	}
	if err := a.Cancel(context.Background(), "new"); err != nil {
		t.Fatal(err)
	}
}

func TestCard(t *testing.T) {
	srv := kagenttest.New()
	defer srv.Close()
	srv.Card.Description = "Keeps the fork rebased."
	card, err := newAgent(t, srv).Card(context.Background())
	if err != nil || card.Description != "Keeps the fork rebased." {
		t.Fatalf("card = %+v, err = %v", card, err)
	}
}

func TestPendingTask(t *testing.T) {
	srv := kagenttest.New()
	defer srv.Close()
	paused := decodeTask(t, askUserTask)
	srv.PutTask(&a2a.Task{ID: "done", ContextID: paused.ContextID, Status: a2a.TaskStatus{State: a2a.TaskStateCompleted}})
	srv.PutTask(paused)
	a := newAgent(t, srv)
	got, err := a.PendingTask(context.Background(), paused.ContextID)
	if err != nil || got.ID != paused.ID {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := a.PendingTask(context.Background(), "other"); !errors.Is(err, kagent.ErrNoRequest) {
		t.Fatalf("err = %v", err)
	}
}

func TestCardSizeIsBounded(t *testing.T) {
	srv := kagenttest.New()
	defer srv.Close()
	srv.Card.Description = strings.Repeat("x", 2<<20)
	if _, err := newAgent(t, srv).Card(context.Background()); err == nil {
		t.Fatal("a 2 MiB agent card was accepted")
	}
}
