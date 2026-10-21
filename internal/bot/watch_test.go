package bot_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/disgoorg/snowflake/v2"

	"github.com/yyewolf/a2a-discord-gtw/internal/kagent"
)

// scheduled is a task as a ScheduledRun leaves it: its prompt in history.
func scheduled(id string, state a2a.TaskState, at time.Time, text string) *a2a.Task {
	t := &a2a.Task{
		ID: a2a.TaskID(id), ContextID: "ctx-" + id,
		History: []*a2a.Message{a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("Do your routine."))},
		Status:  a2a.TaskStatus{State: state, Timestamp: &at},
	}
	if text != "" {
		t.Artifacts = []*a2a.Artifact{{ID: "a", Parts: a2a.ContentParts{a2a.NewTextPart(text)}}}
	}
	return t
}

func TestWatchFirstPollReplaysNothing(t *testing.T) {
	h := newHarness(t)
	h.ensure()
	w := h.agents["windmill-maintainer"]
	w.put(scheduled("old", a2a.TaskStateCompleted, time.Now().Add(-time.Hour), "old news"))
	h.gw.Poll(context.Background())
	if got := h.discord.in(h.channel("windmill-maintainer")); len(got) != 0 {
		t.Fatalf("replayed history: %+v", got)
	}
	if _, ok, _ := h.store.Watermark(context.Background(), "claude-assistant"); ok {
		t.Fatal("an unwatched agent got a watermark")
	}
}

func TestWatchSurfacesScheduledRun(t *testing.T) {
	h := newHarness(t)
	h.ensure()
	ctx := context.Background()
	h.gw.Poll(ctx)
	w := h.agents["windmill-maintainer"]
	ch := h.channel("windmill-maintainer")
	at := time.Now().Add(time.Second)

	// The run pauses on a question.
	paused := scheduled("run1", a2a.TaskStateInputRequired, at, "Rebased onto 1.845.0.")
	paused.Status.Message = a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(""))
	paused.Status.Message.Metadata = map[string]any{kagent.HITLExtension: map[string]any{
		"type": "ask_user_request", "id": "req-1",
		"questions": []any{map[string]any{"question": "Force-push primary?", "choices": []string{"Yes", "No"}}},
	}}
	w.put(paused)
	h.gw.Poll(ctx)

	status := h.discord.in(ch)
	if len(status) != 1 || status[0].Content != "🙋 **windmill-maintainer** · needs input · from kagent\n> Do your routine." {
		t.Fatalf("status = %+v", status)
	}
	thread := snowflake.ID(status[0].ID)
	got := h.discord.in(thread)
	if len(got) != 2 || got[0].Content != "Rebased onto 1.845.0." || !strings.Contains(got[1].Content, "> Force-push primary?") {
		t.Fatalf("thread = %+v", got)
	}
	if !strings.HasPrefix(got[1].Content, "<@"+ownerID.String()+">") {
		t.Fatalf("question does not ping: %q", got[1].Content)
	}

	// Polling again shows nothing new.
	h.gw.Poll(ctx)
	if n := len(h.discord.in(thread)); n != 2 {
		t.Fatalf("%d messages after a quiet poll", n)
	}

	// Answered elsewhere, it completes: only the new text is posted.
	done := scheduled("run1", a2a.TaskStateCompleted, at.Add(time.Second), "Rebased onto 1.845.0. Tagged v1.845.0-fork.1.")
	w.put(done)
	h.gw.Poll(ctx)
	got = h.discord.in(thread)
	if len(got) != 3 || got[2].Content != "Tagged v1.845.0-fork.1." {
		t.Fatalf("thread = %+v", got)
	}
	if s := h.discord.message(status[0].ID).Content; !strings.HasPrefix(s, "✅ **windmill-maintainer** · done") {
		t.Fatalf("status = %q", s)
	}
}

func TestWatchUsesKnownConversation(t *testing.T) {
	h := newHarness(t)
	h.ensure()
	ctx := context.Background()
	h.gw.Poll(ctx)
	w := h.agents["windmill-maintainer"]
	// Started from Discord, then continued after a restart.
	w.onSend = func(*a2a.Message) ([]a2a.Event, error) { return reply("t1", "ctx-1", "first"), nil }
	thread := h.say(h.channel("windmill-maintainer"), ownerID, "hello")
	next := &a2a.Task{
		ID: "t2", ContextID: "ctx-1", Status: a2a.TaskStatus{State: a2a.TaskStateCompleted, Timestamp: new(time.Now().Add(time.Second))},
		Artifacts: []*a2a.Artifact{{ID: "a", Parts: a2a.ContentParts{a2a.NewTextPart("second")}}},
	}
	w.put(next)
	h.gw.Poll(ctx)
	if got := h.discord.in(h.channel("windmill-maintainer")); len(got) != 1 {
		t.Fatalf("a second status message was opened: %+v", got)
	}
	if got := h.discord.in(thread); got[len(got)-1].Content != "second" {
		t.Fatalf("thread = %+v", got)
	}
}
