package bot_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/yyewolf/a2a-discord-gtw/internal/kagent"
	"github.com/yyewolf/a2a-discord-gtw/internal/render"
)

// fakeInteraction is a click or modal submit on a message held by the fake
// Discord.
type fakeInteraction struct {
	user      snowflake.ID
	customID  string
	values    []string
	text      string
	msg       *discord.Message
	acked     bool
	modal     *discord.ModalCreate
	ephemeral []string
}

func (f *fakeInteraction) UserID() snowflake.ID { return f.user }
func (f *fakeInteraction) CustomID() string     { return f.customID }
func (f *fakeInteraction) Values() []string     { return f.values }
func (f *fakeInteraction) Text() string         { return f.text }
func (f *fakeInteraction) Message() (*discord.Message, bool) {
	return f.msg, f.msg != nil
}
func (f *fakeInteraction) Acknowledge() error { f.acked = true; return nil }
func (f *fakeInteraction) Modal(m discord.ModalCreate) error {
	f.modal = &m
	return nil
}

func (f *fakeInteraction) Ephemeral(text string) error {
	f.ephemeral = append(f.ephemeral, text)
	return nil
}

// pausedOn starts a conversation that pauses on payload and returns the
// thread and its request messages.
func pausedOn(t *testing.T, h *harness, payload map[string]any) (snowflake.ID, []fakeMessage) {
	t.Helper()
	msg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(""))
	msg.Metadata = map[string]any{kagent.HITLExtension: payload}
	paused := &a2a.Task{ID: "t1", ContextID: "ctx-1", Status: a2a.TaskStatus{State: a2a.TaskStateInputRequired, Message: msg}}
	h.agents["claude-assistant"].onSend = func(*a2a.Message) ([]a2a.Event, error) { return []a2a.Event{paused}, nil }
	thread := h.say(h.channel("claude-assistant"), ownerID, "go")
	return thread, h.discord.in(thread)
}

// component finds the custom ID of the component of the given kind and
// index on a message.
func component(t *testing.T, m fakeMessage, kind render.Kind, arg int) string {
	t.Helper()
	for _, row := range m.Components {
		for _, c := range row.(discord.ActionRowComponent).Components {
			var custom string
			switch c := c.(type) {
			case discord.ButtonComponent:
				custom = c.CustomID
			case discord.StringSelectMenuComponent:
				custom = c.CustomID
			}
			if id, err := render.ParseID(custom); err == nil && id.Kind == kind && id.Arg == arg {
				return custom
			}
		}
	}
	t.Fatalf("no %s component on %q", kind, m.Content)
	return ""
}

func on(m fakeMessage) *discord.Message {
	return &discord.Message{ID: m.ID, ChannelID: m.ChannelID, Content: m.Content}
}

// captureAnswer makes the agent complete on the next message and returns
// where that message's HITL payload will be stored.
func captureAnswer(h *harness) *map[string]any {
	var payload map[string]any
	h.agents["claude-assistant"].onSend = func(m *a2a.Message) ([]a2a.Event, error) {
		payload, _ = m.Metadata[kagent.HITLExtension].(map[string]any)
		return reply("t1", "ctx-1", "done"), nil
	}
	return &payload
}

func askUser(questions ...map[string]any) map[string]any {
	qs := make([]any, len(questions))
	for i, q := range questions {
		qs[i] = q
	}
	return map[string]any{"type": "ask_user_request", "id": "req-1", "questions": qs}
}

func asJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestAnswerSingleChoice(t *testing.T) {
	h := newHarness(t)
	h.ensure()
	thread, msgs := pausedOn(t, h, askUser(map[string]any{"question": "Red or blue?", "choices": []string{"Red", "Blue"}}))
	payload := captureAnswer(h)
	in := &fakeInteraction{user: ownerID, customID: component(t, msgs[0], render.KindChoice, 1), msg: on(msgs[0])}
	h.gw.HandleInteraction(context.Background(), in)

	if got := asJSON((*payload)["answers"]); got != `[{"answer":["Blue"]}]` {
		t.Fatalf("answers = %s", got)
	}
	if !in.acked {
		t.Fatal("interaction not acknowledged")
	}
	q := h.discord.message(msgs[0].ID)
	if len(q.Components) != 0 || !strings.Contains(q.Content, "↳ **Blue** by <@"+ownerID.String()+">") {
		t.Fatalf("question after answer = %+v", q)
	}
	if got := h.discord.in(thread); got[len(got)-1].Content != "done" {
		t.Fatalf("thread = %+v", got)
	}
}

func TestAnswerRefusals(t *testing.T) {
	cases := []struct {
		name      string
		user      snowflake.ID
		customID  func(msgs []fakeMessage) string
		ephemeral string
		marked    string
	}{
		{
			name: "stranger", user: strangerID,
			customID:  func(msgs []fakeMessage) string { return component(t, msgs[0], render.KindChoice, 0) },
			ephemeral: "Only the agents' owners",
		},
		{
			name: "stale request", user: ownerID,
			customID: func([]fakeMessage) string {
				return render.ID{Kind: render.KindChoice, Task: "t1", Key: render.ShortKey("old-request")}.String()
			},
			marked: "no longer pending",
		},
		{
			name: "unknown task", user: ownerID,
			customID: func([]fakeMessage) string {
				return render.ID{Kind: render.KindChoice, Task: "nope", Key: render.ShortKey("req-1")}.String()
			},
			ephemeral: "do not know this task",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.ensure()
			_, msgs := pausedOn(t, h, askUser(map[string]any{"question": "?", "choices": []string{"A"}}))
			h.agents["claude-assistant"].onSend = func(*a2a.Message) ([]a2a.Event, error) {
				t.Error("an answer was sent")
				return nil, nil
			}
			in := &fakeInteraction{user: c.user, customID: c.customID(msgs), msg: on(msgs[0])}
			h.gw.HandleInteraction(context.Background(), in)
			if c.ephemeral != "" && (len(in.ephemeral) != 1 || !strings.Contains(in.ephemeral[0], c.ephemeral)) {
				t.Fatalf("ephemeral = %q", in.ephemeral)
			}
			if c.marked != "" && !strings.Contains(h.discord.message(msgs[0].ID).Content, c.marked) {
				t.Fatalf("message = %q", h.discord.message(msgs[0].ID).Content)
			}
		})
	}
}

func TestAnswerSeveralQuestions(t *testing.T) {
	h := newHarness(t)
	h.ensure()
	_, msgs := pausedOn(t, h, askUser(
		map[string]any{"question": "Which?", "choices": []string{"a", "b", "c"}, "multiple": true},
		map[string]any{"question": "Why?"},
	))
	if len(msgs) != 2 {
		t.Fatalf("%d question messages", len(msgs))
	}
	h.agents["claude-assistant"].onSend = func(*a2a.Message) ([]a2a.Event, error) {
		t.Error("answer sent before every question was answered")
		return nil, nil
	}
	sel := &fakeInteraction{user: ownerID, customID: component(t, msgs[0], render.KindSelect, 0), values: []string{"0", "2"}, msg: on(msgs[0])}
	h.gw.HandleInteraction(context.Background(), sel)
	if !strings.Contains(h.discord.message(msgs[0].ID).Content, "**a, c** by <@"+ownerID.String()+"> — waiting") {
		t.Fatalf("first = %q", h.discord.message(msgs[0].ID).Content)
	}

	payload := captureAnswer(h)
	open := &fakeInteraction{user: ownerID, customID: component(t, msgs[1], render.KindOther, 0), msg: on(msgs[1])}
	h.gw.HandleInteraction(context.Background(), open)
	if open.modal == nil {
		t.Fatal("no modal opened")
	}
	submit := &fakeInteraction{user: ownerID, customID: open.modal.CustomID, text: " because ", msg: on(msgs[1])}
	h.gw.HandleInteraction(context.Background(), submit)
	if got := asJSON((*payload)["answers"]); got != `[{"answer":["a","c"]},{"answer":["because"]}]` {
		t.Fatalf("answers = %s", got)
	}
}

func TestToolApprovals(t *testing.T) {
	approval := map[string]any{"type": "tool_approval_request", "tools": []any{
		map[string]any{"id": "tool-1", "name": "delete_file"},
		map[string]any{"id": "tool-2", "name": "restart_pod"},
	}}
	cases := []struct {
		name  string
		steps func(t *testing.T, h *harness, m fakeMessage)
		want  string
	}{
		{
			name: "one by one",
			steps: func(t *testing.T, h *harness, m fakeMessage) {
				h.gw.HandleInteraction(context.Background(), &fakeInteraction{user: ownerID, customID: render.ID{Kind: render.KindApprove, Task: "t1", Key: render.ShortKey("tool-1"), Index: 0}.String(), msg: on(m)})
				open := &fakeInteraction{user: ownerID, customID: render.ID{Kind: render.KindReject, Task: "t1", Key: render.ShortKey("tool-1"), Index: 1}.String(), msg: on(m)}
				h.gw.HandleInteraction(context.Background(), open)
				if open.modal == nil {
					t.Fatal("no rejection modal")
				}
				h.gw.HandleInteraction(context.Background(), &fakeInteraction{user: ownerID, customID: open.modal.CustomID, text: "not now", msg: on(m)})
			},
			want: `[{"approved":true,"id":"tool-1"},{"approved":false,"id":"tool-2","rejection_reason":"not now"}]`,
		},
		{
			name: "approve all",
			steps: func(t *testing.T, h *harness, m fakeMessage) {
				h.gw.HandleInteraction(context.Background(), &fakeInteraction{user: ownerID, customID: component(t, m, render.KindApproveAll, 0), msg: on(m)})
			},
			want: `[{"approved":true,"id":"tool-1"},{"approved":true,"id":"tool-2"}]`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.ensure()
			_, msgs := pausedOn(t, h, approval)
			payload := captureAnswer(h)
			c.steps(t, h, msgs[0])
			if got := asJSON((*payload)["approvals"]); got != c.want {
				t.Fatalf("approvals = %s", got)
			}
		})
	}
}

func TestTypedReplyToAnApprovalPostsTheButtons(t *testing.T) {
	h := newHarness(t)
	h.ensure()
	thread, msgs := pausedOn(t, h, map[string]any{"type": "tool_approval_request", "tools": []any{
		map[string]any{"id": "tool-1", "name": "delete_file"},
	}})
	h.agents["claude-assistant"].onSend = func(*a2a.Message) ([]a2a.Event, error) { return nil, errWaiting }
	h.say(thread, ownerID, "yes go ahead")
	got := h.discord.in(thread)
	last := got[len(got)-1]
	if len(got) != len(msgs)+3 || len(last.Components) == 0 || !strings.Contains(last.Content, "wants to run") {
		t.Fatalf("thread = %+v", got)
	}
}

// A resumed turn that streams no text posts only what is new in the task.
func TestResumedTurnPostsOnlyNewText(t *testing.T) {
	h := newHarness(t)
	h.ensure()
	agent := h.agents["claude-assistant"]
	msg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(""))
	msg.Metadata = map[string]any{kagent.HITLExtension: askUser(map[string]any{"question": "Go?", "choices": []string{"Yes", "No"}})}
	before := &a2a.Artifact{ID: "a", Parts: a2a.ContentParts{a2a.NewTextPart("Checked the build.")}}
	agent.onSend = func(*a2a.Message) ([]a2a.Event, error) {
		return []a2a.Event{
			&a2a.TaskArtifactUpdateEvent{TaskID: "t1", ContextID: "ctx-1", Artifact: before},
			&a2a.Task{ID: "t1", ContextID: "ctx-1", Artifacts: []*a2a.Artifact{before}, Status: a2a.TaskStatus{State: a2a.TaskStateInputRequired, Message: msg}},
		}, nil
	}
	thread := h.say(h.channel("claude-assistant"), ownerID, "go")
	got := h.discord.in(thread)
	agent.onSend = func(*a2a.Message) ([]a2a.Event, error) {
		// The resumed stream carries only the final task.
		return []a2a.Event{&a2a.Task{
			ID: "t1", ContextID: "ctx-1", Status: a2a.TaskStatus{State: a2a.TaskStateCompleted},
			Artifacts: []*a2a.Artifact{before, {ID: "b", Parts: a2a.ContentParts{a2a.NewTextPart(" Pushed.")}}},
		}}, nil
	}
	h.gw.HandleInteraction(context.Background(), &fakeInteraction{user: ownerID, customID: component(t, got[len(got)-1], render.KindChoice, 0), msg: on(got[len(got)-1])})
	got = h.discord.in(thread)
	if last := got[len(got)-1].Content; last != "Pushed." {
		t.Fatalf("thread = %+v", got)
	}
	for _, m := range got[1:] {
		if strings.Contains(m.Content, "Checked the build.") {
			t.Fatalf("earlier text posted again: %+v", got)
		}
	}
}

// A double tap sends one answer; the second tap finds it answered.
func TestDoubleTapSendsOneAnswer(t *testing.T) {
	h := newHarness(t)
	h.ensure()
	_, msgs := pausedOn(t, h, map[string]any{"type": "tool_approval_request", "tools": []any{
		map[string]any{"id": "tool-1", "name": "delete_file"},
		map[string]any{"id": "tool-2", "name": "restart_pod"},
	}})
	agent := h.agents["claude-assistant"]
	var mu sync.Mutex
	answers := 0
	agent.onSend = func(m *a2a.Message) ([]a2a.Event, error) {
		mu.Lock()
		answers++
		mu.Unlock()
		time.Sleep(20 * time.Millisecond) // the resumed turn takes a moment
		return reply("t1", "ctx-1", "done"), nil
	}
	custom := component(t, msgs[0], render.KindApproveAll, 0)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			h.gw.HandleInteraction(context.Background(), &fakeInteraction{user: ownerID, customID: custom, msg: on(msgs[0])})
		})
	}
	wg.Wait()
	if answers != 1 {
		t.Fatalf("%d answers sent", answers)
	}
}

// A click queued behind the turn its twin started is checked against the
// request as it is after that turn: here a new question, so it is refused
// rather than sent as an answer to the wrong request.
func TestQueuedClickSeesTheNewRequest(t *testing.T) {
	h := newHarness(t)
	h.ensure()
	_, msgs := pausedOn(t, h, askUser(map[string]any{"question": "First?", "choices": []string{"Yes", "No"}}))
	agent := h.agents["claude-assistant"]
	var mu sync.Mutex
	answers := 0
	agent.onSend = func(*a2a.Message) ([]a2a.Event, error) {
		mu.Lock()
		answers++
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		// The agent asks a follow-up question: a new request on the same task.
		msg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(""))
		msg.Metadata = map[string]any{kagent.HITLExtension: map[string]any{
			"type": "ask_user_request", "id": "req-2",
			"questions": []any{map[string]any{"question": "Second?", "choices": []string{"A", "B"}}},
		}}
		return []a2a.Event{&a2a.Task{ID: "t1", ContextID: "ctx-1", Status: a2a.TaskStatus{State: a2a.TaskStateInputRequired, Message: msg}}}, nil
	}
	custom := component(t, msgs[0], render.KindChoice, 0)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			h.gw.HandleInteraction(context.Background(), &fakeInteraction{user: ownerID, customID: custom, msg: on(msgs[0])})
		})
	}
	wg.Wait()
	if answers != 1 {
		t.Fatalf("%d answers sent", answers)
	}
}

// The modal opens without waiting on kagent, which may be slow or down.
func TestModalOpensWithoutKagent(t *testing.T) {
	h := newHarness(t)
	h.ensure()
	_, msgs := pausedOn(t, h, askUser(map[string]any{"question": "Why?"}))
	h.agents["claude-assistant"].taskErr["t1"] = errors.New("kagent down")
	in := &fakeInteraction{user: ownerID, customID: component(t, msgs[0], render.KindOther, 0), msg: on(msgs[0])}
	h.gw.HandleInteraction(context.Background(), in)
	if in.modal == nil || len(in.ephemeral) != 0 {
		t.Fatalf("modal = %v, ephemeral = %q", in.modal, in.ephemeral)
	}
}
