package bot_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/yyewolf/a2a-discord-gtw/internal/bot"
	"github.com/yyewolf/a2a-discord-gtw/internal/kagent"
	"github.com/yyewolf/a2a-discord-gtw/internal/render"
)

// errWaiting is kagent's refusal of a plain turn while a question is pending.
var errWaiting = a2a.NewError(a2a.ErrUnsupportedOperation, "native session is waiting for another task")

func textArtifact(id, text string) *a2a.TaskArtifactUpdateEvent {
	return &a2a.TaskArtifactUpdateEvent{Artifact: &a2a.Artifact{ID: a2a.ArtifactID(id), Parts: a2a.ContentParts{a2a.NewTextPart(text)}}}
}

// reply is a completed turn streamed as kagent does: the task, working,
// the reply as artifacts, then the final task.
func reply(taskID, contextID string, chunks ...string) []a2a.Event {
	working := &a2a.Task{ID: a2a.TaskID(taskID), ContextID: contextID, Status: a2a.TaskStatus{State: a2a.TaskStateSubmitted}}
	evs := []a2a.Event{working, &a2a.TaskStatusUpdateEvent{TaskID: working.ID, ContextID: contextID, Status: a2a.TaskStatus{State: a2a.TaskStateWorking}}}
	final := &a2a.Task{ID: working.ID, ContextID: contextID, Status: a2a.TaskStatus{State: a2a.TaskStateCompleted}}
	for i, c := range chunks {
		a := textArtifact(string(rune('a'+i)), c)
		a.TaskID, a.ContextID = working.ID, contextID
		evs = append(evs, a)
		final.Artifacts = append(final.Artifacts, a.Artifact)
	}
	return append(evs, final)
}

// question is a turn that pauses on an ask-user request.
func question(taskID, contextID string, q kagent.Question) []a2a.Event {
	payload := map[string]any{"type": "ask_user_request", "id": "req-" + taskID, "questions": []any{map[string]any{
		"question": q.Question, "choices": q.Choices, "multiple": q.Multiple,
	}}}
	msg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(""))
	msg.Metadata = map[string]any{kagent.HITLExtension: payload}
	paused := &a2a.Task{ID: a2a.TaskID(taskID), ContextID: contextID, Status: a2a.TaskStatus{State: a2a.TaskStateInputRequired, Message: msg}}
	return []a2a.Event{
		&a2a.Task{ID: paused.ID, ContextID: contextID, Status: a2a.TaskStatus{State: a2a.TaskStateWorking}},
		paused,
	}
}

func (h *harness) say(ch snowflake.ID, author snowflake.ID, content string) snowflake.ID {
	h.t.Helper()
	id := h.discord.userMessage(ch, content)
	h.gw.HandleMessage(context.Background(), bot.Message{ID: id, ChannelID: ch, GuildID: guildID, AuthorID: author, Content: content})
	return id
}

func TestMessageStartsConversation(t *testing.T) {
	h := newHarness(t)
	h.ensure()
	agent := h.agents["claude-assistant"]
	agent.onSend = func(m *a2a.Message) ([]a2a.Event, error) {
		if m.ContextID != "" {
			t.Errorf("first turn carried context %q", m.ContextID)
		}
		return reply("t1", "ctx-1", "There are ", "52 namespaces."), nil
	}
	ch := h.channel("claude-assistant")
	msgID := h.say(ch, ownerID, "how many namespaces?")

	// Discord threads opened on a message take its ID.
	thread := h.discord.in(msgID)
	if len(thread) != 1 || thread[0].Content != "There are 52 namespaces." {
		t.Fatalf("thread = %+v", thread)
	}
	if got := h.discord.message(msgID).Reactions; len(got) != 1 || got[0] != "✅" {
		t.Fatalf("reactions = %v", got)
	}
	conv, ok, err := h.store.ConversationByContext(context.Background(), "claude-assistant", "ctx-1")
	if !ok || err != nil || conv.ThreadID != uint64(msgID) || conv.Title != "how many namespaces?" || conv.State != string(a2a.TaskStateCompleted) {
		t.Fatalf("conversation = %+v, %v, %v", conv, ok, err)
	}

	agent.onSend = func(m *a2a.Message) ([]a2a.Event, error) {
		if m.ContextID != "ctx-1" {
			t.Errorf("reply context = %q", m.ContextID)
		}
		return reply("t2", "ctx-1", "Still 52."), nil
	}
	h.say(msgID, ownerID, "and now?")
	if got := h.discord.in(msgID); got[len(got)-1].Content != "Still 52." {
		t.Fatalf("thread = %+v", got)
	}
}

func TestMessagesThatAreIgnored(t *testing.T) {
	cases := []struct {
		name string
		send func(h *harness) bot.Message
	}{
		{"stranger", func(h *harness) bot.Message {
			return bot.Message{ChannelID: h.channel("claude-assistant"), GuildID: guildID, AuthorID: strangerID, Content: "hi"}
		}},
		{"bot", func(h *harness) bot.Message {
			return bot.Message{ChannelID: h.channel("claude-assistant"), GuildID: guildID, AuthorID: ownerID, AuthorBot: true, Content: "hi"}
		}},
		{"other guild", func(h *harness) bot.Message {
			return bot.Message{ChannelID: h.channel("claude-assistant"), GuildID: 42, AuthorID: ownerID, Content: "hi"}
		}},
		{"other channel", func(*harness) bot.Message {
			return bot.Message{ChannelID: 4242, GuildID: guildID, AuthorID: ownerID, Content: "hi"}
		}},
		{"empty", func(h *harness) bot.Message {
			return bot.Message{ChannelID: h.channel("claude-assistant"), GuildID: guildID, AuthorID: ownerID, Content: "  "}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.ensure()
			h.agents["claude-assistant"].onSend = func(*a2a.Message) ([]a2a.Event, error) {
				t.Error("the agent was called")
				return nil, nil
			}
			m := c.send(h)
			m.ID = h.discord.userMessage(m.ChannelID, m.Content)
			h.gw.HandleMessage(context.Background(), m)
		})
	}
}

func TestLongReplyIsSplit(t *testing.T) {
	h := newHarness(t)
	h.ensure()
	long := strings.Repeat("word ", 900) // 4500 characters
	h.agents["claude-assistant"].onSend = func(*a2a.Message) ([]a2a.Event, error) {
		return reply("t1", "ctx-1", long), nil
	}
	msgID := h.say(h.channel("claude-assistant"), ownerID, "talk")
	got := h.discord.in(msgID)
	if len(got) != 3 {
		t.Fatalf("%d messages", len(got))
	}
	for _, m := range got {
		if len([]rune(m.Content)) > render.MessageLimit {
			t.Fatalf("message of %d characters", len([]rune(m.Content)))
		}
	}
}

func TestFailedTaskShowsReason(t *testing.T) {
	h := newHarness(t)
	h.ensure()
	h.agents["claude-assistant"].onSend = func(*a2a.Message) ([]a2a.Event, error) {
		failed := &a2a.Task{ID: "t1", ContextID: "ctx-1", Status: a2a.TaskStatus{
			State: a2a.TaskStateFailed, Message: a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("model unavailable")),
		}}
		return []a2a.Event{failed}, nil
	}
	msgID := h.say(h.channel("claude-assistant"), ownerID, "hi")
	if got := h.discord.in(msgID); len(got) != 1 || got[0].Content != "model unavailable" {
		t.Fatalf("thread = %+v", got)
	}
	if got := h.discord.message(msgID).Reactions; len(got) != 1 || got[0] != "❌" {
		t.Fatalf("reactions = %v", got)
	}
}

func TestStreamErrorIsReported(t *testing.T) {
	h := newHarness(t)
	h.ensure()
	h.agents["claude-assistant"].onSend = func(*a2a.Message) ([]a2a.Event, error) {
		return reply("t1", "ctx-1", "partial")[:3], errors.New("connection reset")
	}
	msgID := h.say(h.channel("claude-assistant"), ownerID, "hi")
	got := h.discord.in(msgID)
	if len(got) != 2 || !strings.Contains(got[1].Content, "connection reset") {
		t.Fatalf("thread = %+v", got)
	}
}

func TestQuestionIsAskedAndTypedAnswerSent(t *testing.T) {
	h := newHarness(t)
	h.ensure()
	agent := h.agents["claude-assistant"]
	agent.onSend = func(*a2a.Message) ([]a2a.Event, error) {
		return question("t1", "ctx-1", kagent.Question{Question: "Red or blue?", Choices: []string{"Red", "Blue"}}), nil
	}
	msgID := h.say(h.channel("claude-assistant"), ownerID, "ask me")

	thread := h.discord.in(msgID)
	if len(thread) != 1 || !strings.Contains(thread[0].Content, "<@"+ownerID.String()+">") || !strings.Contains(thread[0].Content, "> Red or blue?") {
		t.Fatalf("thread = %+v", thread)
	}
	if m := thread[0].Mentions; m == nil || len(m.Users) != 1 || m.Users[0] != ownerID {
		t.Fatalf("mentions = %+v", m)
	}
	if n := len(thread[0].Components[0].(discord.ActionRowComponent).Components); n != 2 {
		t.Fatalf("%d choice buttons", n)
	}
	if got := h.discord.message(msgID).Reactions; len(got) != 1 || got[0] != "🙋" {
		t.Fatalf("reactions = %v", got)
	}

	var answer *a2a.Message
	agent.onSend = func(m *a2a.Message) ([]a2a.Event, error) {
		if m.TaskID == "" {
			return nil, errWaiting
		}
		answer = m
		return reply("t1", "ctx-1", "YOU PICKED Blue."), nil
	}
	h.say(msgID, ownerID, "blue")
	if answer == nil || answer.TaskID != "t1" {
		t.Fatalf("answer = %+v", answer)
	}
	payload := answer.Metadata[kagent.HITLExtension].(map[string]any)
	if got := payload["answers"].([]any)[0].(map[string]any)["answer"].([]any)[0]; got != "Blue" {
		t.Fatalf("answer payload = %v", payload)
	}
	if got := h.discord.in(msgID); got[len(got)-1].Content != "YOU PICKED Blue." {
		t.Fatalf("thread = %+v", got)
	}
	if got := h.discord.message(msgID).Reactions; len(got) != 1 || got[0] != "✅" {
		t.Fatalf("reactions = %v", got)
	}
}

func TestQuestionAskedOnce(t *testing.T) {
	h := newHarness(t)
	h.ensure()
	h.agents["claude-assistant"].onSend = func(*a2a.Message) ([]a2a.Event, error) {
		return question("t1", "ctx-1", kagent.Question{Question: "Why?"}), nil
	}
	msgID := h.say(h.channel("claude-assistant"), ownerID, "ask me")
	rec, ok, err := h.store.Task(context.Background(), "t1")
	if !ok || err != nil || rec.RequestKey != "req-t1" || rec.State != string(a2a.TaskStateInputRequired) {
		t.Fatalf("task record = %+v, %v, %v", rec, ok, err)
	}
	if n := len(h.discord.in(msgID)); n != 1 {
		t.Fatalf("%d messages", n)
	}
}
