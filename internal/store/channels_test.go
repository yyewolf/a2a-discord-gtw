package store_test

import (
	"context"
	"testing"
)

func TestAgentChannel(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if _, ok, err := s.AgentChannel(ctx, "windmill-maintainer"); ok || err != nil {
		t.Fatalf("ok = %v, err = %v", ok, err)
	}
	for _, id := range []uint64{1562536811325300746, 1562536811325300999} {
		if err := s.SetAgentChannel(ctx, "windmill-maintainer", id); err != nil {
			t.Fatal(err)
		}
		got, ok, err := s.AgentChannel(ctx, "windmill-maintainer")
		if !ok || err != nil || got != id {
			t.Fatalf("got %d, %v, %v; want %d", got, ok, err, id)
		}
	}
}
