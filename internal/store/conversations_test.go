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
	c.ThreadID++
	if err := s.PutConversation(ctx, c); err == nil {
		t.Fatal("a context was bound to a second thread")
	}
}
