package store_test

import (
	"context"
	"testing"

	"github.com/yyewolf/a2a-discord-gtw/internal/store"
)

func TestRequestMessages(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	for _, m := range []store.RequestMessage{{ChannelID: 1, MessageID: 10}, {ChannelID: 1, MessageID: 11}, {ChannelID: 1, MessageID: 10}} {
		if err := s.AddRequestMessage(ctx, "t", m); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.AddRequestMessage(ctx, "other", store.RequestMessage{ChannelID: 2, MessageID: 20})
	got, err := s.TakeRequestMessages(ctx, "t")
	if err != nil || len(got) != 2 {
		t.Fatalf("got %v, %v", got, err)
	}
	if again, _ := s.TakeRequestMessages(ctx, "t"); len(again) != 0 {
		t.Fatalf("taken twice: %v", again)
	}
}
