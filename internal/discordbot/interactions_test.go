package discordbot

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"

	"github.com/yyewolf/a2a-discord-gtw/internal/bot"
)

func TestComponentInteractionMapping(t *testing.T) {
	cases := []struct {
		name   string
		data   discord.ComponentInteractionData
		values []string
	}{
		{"button", discord.ButtonInteractionData{}, nil},
		{"select", discord.StringSelectMenuInteractionData{Values: []string{"0", "2"}}, []string{"0", "2"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := &events.ComponentInteractionCreate{ComponentInteraction: discord.ComponentInteraction{
				Data: c.data, Message: discord.Message{Content: "q"},
			}}
			in := &componentInteraction{e: e}
			if got := in.Values(); len(got) != len(c.values) {
				t.Fatalf("values = %v", got)
			}
			if m, ok := in.Message(); !ok || m.Content != "q" {
				t.Fatalf("message = %v, %v", m, ok)
			}
			if in.Text() != "" {
				t.Fatal("a component has no text")
			}
		})
	}
}

func TestModalInteractionWithoutMessage(t *testing.T) {
	in := &modalInteraction{e: &events.ModalSubmitInteractionCreate{}}
	if _, ok := in.Message(); ok {
		t.Fatal("a modal not opened from a message has none")
	}
	if in.Text() != "" || in.Values() != nil {
		t.Fatal("empty modal yields values")
	}
	if err := in.Modal(discord.ModalCreate{}); err == nil {
		t.Fatal("a modal opened a modal")
	}
}

func TestResponded(t *testing.T) {
	var r responded
	if r.is() {
		t.Fatal("fresh interaction marked responded")
	}
	r.mark()
	if !r.is() {
		t.Fatal("mark had no effect")
	}
}

func TestHandlerPanicIsContained(t *testing.T) {
	var logs strings.Builder
	b := &Bot{log: slog.New(slog.NewTextHandler(&logs, nil))}
	b.run(func() { panicky{}.HandleMessage(context.Background(), bot.Message{}) })
	b.work.Wait()
	if !strings.Contains(logs.String(), "event handler panicked") {
		t.Fatalf("logs = %q", logs.String())
	}
}

type panicky struct{ Handler }

func (panicky) HandleMessage(context.Context, bot.Message) { panic("boom") }
