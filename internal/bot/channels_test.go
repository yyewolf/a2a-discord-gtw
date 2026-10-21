package bot_test

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestEnsureChannelsCreatesWithTopic(t *testing.T) {
	h := newHarness(t)
	h.ensure()
	for _, name := range []string{"claude-assistant", "windmill-maintainer"} {
		ch, ok := h.discord.channelByName(name)
		if !ok {
			t.Fatalf("%s: no channel", name)
		}
		if ch.ParentID() == nil || *ch.ParentID() != categoryID {
			t.Errorf("%s: parent = %v", name, ch.ParentID())
		}
		if ch.Topic() == nil || *ch.Topic() != "The "+name+" agent." {
			t.Errorf("%s: topic = %v", name, ch.Topic())
		}
	}
}

func TestEnsureChannelsReusesAndRecreates(t *testing.T) {
	cases := []struct {
		name   string
		before func(h *harness)
		// wantSame: the second run keeps the first channel.
		wantSame bool
	}{
		{name: "restart", before: func(*harness) {}, wantSame: true},
		{name: "deleted", before: func(h *harness) { h.discord.deleteChannel(h.channel("claude-assistant")) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.ensure()
			first := h.channel("claude-assistant")
			c.before(h)
			h.ensure()
			second := h.channel("claude-assistant")
			if (first == second) != c.wantSame {
				t.Fatalf("first %s, second %s", first, second)
			}
			if got, _, _ := h.store.AgentChannel(context.Background(), "claude-assistant"); got != uint64(second) {
				t.Fatalf("stored %d, want %s", got, second)
			}
		})
	}
}

func TestEnsureChannelsAdoptsExisting(t *testing.T) {
	h := newHarness(t)
	existing := h.discord.addTextChannel("claude-assistant", categoryID, "mine")
	other := h.discord.addTextChannel("windmill-maintainer", 999, "") // outside the category
	h.ensure()
	if got := h.channel("claude-assistant"); got != existing {
		t.Fatalf("claude-assistant = %s, want adopted %s", got, existing)
	}
	stored, _, _ := h.store.AgentChannel(context.Background(), "windmill-maintainer")
	if stored == uint64(other) {
		t.Fatal("adopted a channel outside the category")
	}
}

func TestEnsureChannelsWithoutCard(t *testing.T) {
	h := newHarness(t)
	h.agents["claude-assistant"].cardErr = errors.New("unreachable")
	h.ensure()
	if ch, _ := h.discord.channelByName("claude-assistant"); ch.Topic() != nil && *ch.Topic() != "" {
		t.Fatalf("topic = %q", *ch.Topic())
	}
}

// One agent's channel failing does not stop the others, and says why.
func TestEnsureChannelsKeepsGoing(t *testing.T) {
	h := newHarness(t)
	h.discord.failChannel = func(name string) error {
		if name == "claude-assistant" {
			return missingPermissions()
		}
		return nil
	}
	err := h.gw.EnsureChannels(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Manage Channels on category "+categoryID.String()) {
		t.Fatalf("err = %v", err)
	}
	if _, ok := h.discord.channelByName("windmill-maintainer"); !ok {
		t.Fatal("the other agent got no channel")
	}
	if err := h.gw.ChannelsReady(context.Background()); err == nil || !strings.Contains(err.Error(), "claude-assistant") {
		t.Fatalf("ready = %v", err)
	}
}
