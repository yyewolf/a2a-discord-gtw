//go:build live

package kagent_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/yyewolf/a2a-discord-gtw/internal/kagent"
)

// TestLive runs the question round trip against a real kagent agent:
//
//	ADG_LIVE_AGENT_URL=http://127.0.0.1:8083/agents/kagent/claude-assistant \
//	  go test -tags live -run TestLive -v ./internal/kagent
//
// The agent must have an ask-user tool (the Claude harness does).
func TestLive(t *testing.T) {
	url := os.Getenv("ADG_LIVE_AGENT_URL")
	if url == "" {
		t.Skip("ADG_LIVE_AGENT_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	a, err := kagent.NewAgent(ctx, url, kagent.Options{})
	if err != nil {
		t.Fatal(err)
	}
	card, err := a.Card(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("card: %s — %s", card.Name, card.Description)

	start := time.Now().Add(-time.Second)
	last := stream(ctx, t, a, kagent.TextMessage("", "Before answering anything, use your AskUserQuestion tool to ask me whether I prefer 'red' or 'blue' (single choice). After I answer, reply with exactly: YOU PICKED <answer>."))
	req, err := kagent.PendingRequest(last)
	if err != nil {
		t.Fatalf("state %s: %v", last.Status.State, err)
	}
	t.Logf("question: %+v", req.Questions)
	defer func() {
		if t.Failed() {
			_ = a.Cancel(context.Background(), last.ID)
		}
	}()

	for ev, err := range a.Stream(ctx, kagent.TextMessage(last.ContextID, "blue")) {
		if err == nil {
			t.Fatalf("plain reply accepted: %T", ev)
		}
		if !kagent.IsWaitingForInput(err) {
			t.Fatalf("plain reply err = %v", err)
		}
		break
	}

	tasks, err := a.TasksSince(ctx, start)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tk := range tasks {
		found = found || tk.ID == last.ID
	}
	if !found {
		t.Fatalf("ListTasks since %s did not return %s (%d tasks)", start, last.ID, len(tasks))
	}

	answer, err := kagent.AnswerMessage(last, req, [][]string{{req.Questions[0].Choices[len(req.Questions[0].Choices)-1]}}, "")
	if err != nil {
		t.Fatal(err)
	}
	final := stream(ctx, t, a, answer)
	if final.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("final state %s", final.Status.State)
	}
	got, err := a.Task(ctx, final.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("reply: %q", kagent.TaskText(got))
}

func stream(ctx context.Context, t *testing.T, a *kagent.Agent, m *a2a.Message) *a2a.Task {
	t.Helper()
	var tr kagent.Transcript
	var last *a2a.Task
	n := 0
	for ev, err := range a.Stream(ctx, m) {
		if err != nil {
			t.Fatal(err)
		}
		n++
		t.Logf("event %T", ev)
		switch e := ev.(type) {
		case *a2a.Task:
			last = e
		case *a2a.TaskArtifactUpdateEvent:
			tr.Add(e)
		case *a2a.TaskStatusUpdateEvent:
			t.Logf("status %s", e.Status.State)
			if last != nil {
				last.Status = e.Status
			} else {
				last = &a2a.Task{ID: e.TaskID, ContextID: e.ContextID, Status: e.Status}
			}
		}
	}
	t.Logf("%d events, streamed text %q", n, tr.Text())
	if last == nil {
		t.Fatal("no task in stream")
	}
	return last
}
