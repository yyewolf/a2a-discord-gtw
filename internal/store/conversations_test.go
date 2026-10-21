package store_test

import (
	"context"
	"testing"

	"github.com/yyewolf/a2a-discord-gtw/internal/store"
)

func TestConversations(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	c := store.Conversation{
		ThreadID: 1562536811325300746, Agent: "claude-assistant", ContextID: "ctx-1",
		ChannelID: 1562536811325300700, StatusMessageID: 1562536811325300746, Title: "how many namespaces?",
		Origin: "<@1>",
	}
	if err := s.PutConversation(ctx, c); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.ConversationByThread(ctx, c.ThreadID)
	if !ok || err != nil || got.ContextID != "ctx-1" || got.ChannelID != c.ChannelID || got.CreatedAt.IsZero() {
		t.Fatalf("by thread = %+v, %v, %v", got, ok, err)
	}
	got, ok, err = s.ConversationByContext(ctx, "claude-assistant", "ctx-1")
	if !ok || err != nil || got.ThreadID != c.ThreadID {
		t.Fatalf("by context = %+v, %v, %v", got, ok, err)
	}
	if _, ok, _ := s.ConversationByContext(ctx, "windmill-maintainer", "ctx-1"); ok {
		t.Fatal("context matched across agents")
	}
	if got.OwnStatus || got.Origin != "<@1>" || got.State != "" {
		t.Fatalf("status fields = %+v", got)
	}
	if err := s.SetConversationState(ctx, c.ThreadID, "TASK_STATE_COMPLETED"); err != nil {
		t.Fatal(err)
	}
	if got, _, _ = s.ConversationByThread(ctx, c.ThreadID); got.State != "TASK_STATE_COMPLETED" {
		t.Fatalf("state = %q", got.State)
	}
	c.ThreadID++
	if err := s.PutConversation(ctx, c); err == nil {
		t.Fatal("a context was bound to a second thread")
	}
}

func TestConversationWithoutContextYet(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	// Two threads opened before kagent answered either: no context yet.
	for _, thread := range []uint64{1, 2} {
		if err := s.PutConversation(ctx, store.Conversation{ThreadID: thread, Agent: "a", ChannelID: 9, StatusMessageID: thread, Title: "t"}); err != nil {
			t.Fatal(err)
		}
	}
	if c, ok, err := s.ConversationByThread(ctx, 1); !ok || err != nil || c.ContextID != "" {
		t.Fatalf("got %+v, %v, %v", c, ok, err)
	}
	if err := s.SetConversationContext(ctx, 1, "ctx-1"); err != nil {
		t.Fatal(err)
	}
	if c, ok, _ := s.ConversationByContext(ctx, "a", "ctx-1"); !ok || c.ThreadID != 1 {
		t.Fatalf("by context = %+v, %v", c, ok)
	}
	if err := s.SetConversationContext(ctx, 2, "ctx-1"); err == nil {
		t.Fatal("a context was bound to two threads")
	}
}
