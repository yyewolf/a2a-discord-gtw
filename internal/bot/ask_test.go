package bot_test

import (
	"context"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/disgoorg/snowflake/v2"

	"github.com/yyewolf/a2a-discord-gtw/internal/bot"
)

func TestAsk(t *testing.T) {
	cases := []struct {
		name      string
		ask       func(h *harness) bot.Ask
		wantAgent string // empty: refused
		wantReply string
	}{
		{
			name: "agent option anywhere",
			ask: func(*harness) bot.Ask {
				return bot.Ask{UserID: ownerID, ChannelID: 4242, Agent: "windmill-maintainer", Prompt: "status?"}
			},
			wantAgent: "windmill-maintainer", wantReply: "Started in <#",
		},
		{
			name: "channel's agent by default",
			ask: func(h *harness) bot.Ask {
				return bot.Ask{UserID: ownerID, ChannelID: h.channel("claude-assistant"), Prompt: "status?"}
			},
			wantAgent: "claude-assistant", wantReply: "Started in <#",
		},
		{
			name:      "no agent outside agent channels",
			ask:       func(*harness) bot.Ask { return bot.Ask{UserID: ownerID, ChannelID: 4242, Prompt: "status?"} },
			wantReply: "pick one with the `agent` option",
		},
		{
			name:      "unknown agent",
			ask:       func(*harness) bot.Ask { return bot.Ask{UserID: ownerID, Agent: "nope", Prompt: "status?"} },
			wantReply: "no agent called **nope**",
		},
		{
			name: "stranger",
			ask: func(*harness) bot.Ask {
				return bot.Ask{UserID: strangerID, Agent: "claude-assistant", Prompt: "status?"}
			},
			wantReply: "Only the agents' owners",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.ensure()
			called := ""
			for name, a := range h.agents {
				a.onSend = func(*a2a.Message) ([]a2a.Event, error) {
					called = name
					return reply("t1", "ctx-1", "all good"), nil
				}
			}
			var replies []string
			h.gw.HandleAsk(context.Background(), c.ask(h), func(text string) error {
				replies = append(replies, text)
				return nil
			})
			if len(replies) != 1 || !strings.Contains(replies[0], c.wantReply) {
				t.Fatalf("replies = %q", replies)
			}
			if called != c.wantAgent {
				t.Fatalf("agent called = %q, want %q", called, c.wantAgent)
			}
			if c.wantAgent == "" {
				return
			}
			status := h.discord.in(h.channel(c.wantAgent))
			if len(status) != 1 || status[0].Content != "✅ **"+c.wantAgent+"** · done · <@"+ownerID.String()+">\n> status?" {
				t.Fatalf("status messages = %+v", status)
			}
			if got := h.discord.in(snowflake.ID(status[0].ID)); len(got) != 1 || got[0].Content != "all good" {
				t.Fatalf("thread = %+v", got)
			}
		})
	}
}

func TestCommandsListAgents(t *testing.T) {
	h := newHarness(t)
	cmds := h.gw.Commands()
	if len(cmds) != 1 || cmds[0].CommandName() != bot.AskCommand {
		t.Fatalf("commands = %+v", cmds)
	}
}
