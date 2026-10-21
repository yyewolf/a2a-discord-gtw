package bot_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/yyewolf/a2a-discord-gtw/internal/bot"

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
	thread := status[0].ID
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
	// Answered elsewhere, the question here loses its buttons.
	if q := h.discord.message(got[1].ID); len(q.Components) != 0 {
		t.Fatalf("question still has %d component rows", len(q.Components))
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

// A task that fails to surface is retried, together with every task after
// it, whatever order the server lists them in.
func TestWatchRetriesAFailedTask(t *testing.T) {
	h := newHarness(t)
	h.ensure()
	ctx := context.Background()
	h.gw.Poll(ctx)
	w := h.agents["windmill-maintainer"]
	at := time.Now().Add(time.Second)
	w.put(scheduled("older", a2a.TaskStateCompleted, at, "first run"))
	w.put(scheduled("newer", a2a.TaskStateCompleted, at.Add(time.Second), "second run"))
	w.taskErr["older"] = errors.New("kagent hiccup")

	h.gw.Poll(ctx)
	h.gw.Poll(ctx)

	var seen []string
	for _, m := range h.discord.in(h.channel("windmill-maintainer")) {
		for _, r := range h.discord.in(m.ID) {
			seen = append(seen, r.Content)
		}
	}
	if !slices.Equal(seen, []string{"first run", "second run"}) {
		t.Fatalf("surfaced %q", seen)
	}
}

// A conversation started from Discord and the watcher racing for its new
// context end with one thread, never a second "from kagent" one.
func TestWatchDoesNotDuplicateAStartingConversation(t *testing.T) {
	for i := range 20 {
		h := newHarness(t)
		h.ensure()
		ctx := context.Background()
		h.gw.Poll(ctx)
		w := h.agents["windmill-maintainer"]
		ctxID := fmt.Sprintf("ctx-%d", i)
		w.onSend = func(*a2a.Message) ([]a2a.Event, error) {
			// kagent has the task before the bot sees the first event.
			w.put(&a2a.Task{ID: "t1", ContextID: ctxID, Status: a2a.TaskStatus{State: a2a.TaskStateWorking, Timestamp: new(time.Now().Add(time.Second))}})
			return reply("t1", ctxID, "hi"), nil
		}
		done := make(chan struct{})
		var wg sync.WaitGroup
		wg.Go(func() {
			for {
				select {
				case <-done:
					return
				default:
					h.gw.Poll(ctx)
				}
			}
		})
		h.say(h.channel("windmill-maintainer"), ownerID, "go")
		close(done)
		wg.Wait()
		h.gw.Poll(ctx)
		for _, c := range h.discord.everCreated() {
			if strings.Contains(c, originKagentText) {
				t.Fatalf("iteration %d: duplicate conversation %q", i, c)
			}
		}
	}
}

const originKagentText = "from kagent"

// A poll while a conversation started from Discord has no context yet leaves
// its task for later instead of opening a second thread for it.
func TestWatchDefersWhileAConversationStarts(t *testing.T) {
	h := newHarness(t)
	h.ensure()
	ctx := context.Background()
	h.gw.Poll(ctx)
	w := h.agents["windmill-maintainer"]
	w.onSend = func(*a2a.Message) ([]a2a.Event, error) {
		// kagent already lists the task; the bot has not seen an event.
		w.put(&a2a.Task{ID: "t1", ContextID: "ctx-1", Status: a2a.TaskStatus{State: a2a.TaskStateWorking, Timestamp: new(time.Now().Add(time.Second))}})
		h.gw.Poll(ctx)
		return reply("t1", "ctx-1", "hi"), nil
	}
	thread := h.say(h.channel("windmill-maintainer"), ownerID, "go")
	h.gw.Poll(ctx)
	for _, c := range h.discord.everCreated() {
		if strings.Contains(c, originKagentText) {
			t.Fatalf("duplicate conversation %q", c)
		}
	}
	if got := h.discord.in(thread); len(got) != 1 || got[0].Content != "hi" {
		t.Fatalf("thread = %+v", got)
	}
}

// While a conversation is busy with a turn, polling does not wait for it,
// and what changed is shown once the turn is over.
func TestWatchSkipsABusyConversation(t *testing.T) {
	h := newHarness(t)
	h.ensure()
	ctx := context.Background()
	h.gw.Poll(ctx)
	w := h.agents["windmill-maintainer"]
	release := make(chan struct{})
	entered := make(chan struct{})
	w.onSend = func(*a2a.Message) ([]a2a.Event, error) {
		close(entered)
		<-release
		return reply("t1", "ctx-1", "turn reply"), nil
	}
	var wg sync.WaitGroup
	thread := h.discord.userMessage(h.channel("windmill-maintainer"), "go")
	wg.Go(func() {
		h.gw.HandleMessage(ctx, bot.Message{ID: thread, ChannelID: h.channel("windmill-maintainer"), GuildID: guildID, AuthorID: ownerID, Content: "go"})
	})
	<-entered
	// The context gets named before the turn's events reach the bot.
	if err := h.store.SetConversationContext(ctx, uint64(thread), "ctx-1"); err != nil {
		t.Fatal(err)
	}
	w.put(&a2a.Task{
		ID: "t2", ContextID: "ctx-1", Status: a2a.TaskStatus{State: a2a.TaskStateCompleted, Timestamp: new(time.Now().Add(time.Second))},
		Artifacts: []*a2a.Artifact{{ID: "a", Parts: a2a.ContentParts{a2a.NewTextPart("side task")}}},
	})

	polled := make(chan struct{})
	go func() {
		h.gw.Poll(ctx)
		close(polled)
	}()
	select {
	case <-polled:
	case <-time.After(5 * time.Second):
		t.Fatal("polling waited for a busy conversation")
	}
	close(release)
	wg.Wait()
	h.gw.Poll(ctx)
	h.gw.Poll(ctx)
	var side int
	for _, m := range h.discord.in(thread) {
		if m.Content == "side task" {
			side++
		}
	}
	if side != 1 {
		t.Fatalf("side task posted %d times: %+v", side, h.discord.in(thread))
	}
}
