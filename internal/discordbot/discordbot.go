// Package discordbot connects the gateway to Discord with DisGo: it opens the
// gateway connection, registers the slash commands, and turns DisGo events
// into calls on the gateway.
package discordbot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"

	"github.com/disgoorg/disgo"
	disgobot "github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/yyewolf/a2a-discord-gtw/internal/bot"
)

// Handler is what the bot forwards events to. *bot.Gateway implements it.
type Handler interface {
	HandleMessage(ctx context.Context, m bot.Message)
	HandleInteraction(ctx context.Context, in bot.Interaction)
	HandleAsk(ctx context.Context, a bot.Ask, respond func(text string) error)
	HandleChannelChange(ctx context.Context, channel snowflake.ID)
	Commands() []discord.ApplicationCommandCreate
}

// Bot is a DisGo client bound to a Handler. Each event is handled in its own
// goroutine, since a handler returns only when the agent's turn is over.
type Bot struct {
	client *disgobot.Client
	log    *slog.Logger
	// closeClient closes the DisGo client; a field so tests can watch the
	// shutdown order.
	closeClient func(ctx context.Context)
	work        sync.WaitGroup
}

// New builds the client. It does not connect.
func New(token string, log *slog.Logger) (*Bot, error) {
	client, err := disgo.New(token,
		disgobot.WithLogger(disgoLogger(log)),
		disgobot.WithGatewayConfigOpts(gateway.WithIntents(
			gateway.IntentGuilds,
			gateway.IntentGuildMessages,
			gateway.IntentMessageContent,
		)),
		// Everything the gateway needs comes with the events or from its
		// own store; nothing is read from DisGo's caches.
		disgobot.WithCacheConfigOpts(cache.WithCaches(cache.FlagsNone)),
	)
	if err != nil {
		return nil, fmt.Errorf("discordbot: %w", err)
	}
	return &Bot{client: client, log: log, closeClient: client.Close}, nil
}

// Rest is the REST client, for the gateway.
func (b *Bot) Rest() rest.Rest { return b.client.Rest }

// Start registers the handler's commands in the guild, subscribes h to the
// events and connects. Events are handled under ctx, which should be
// cancelled before Close.
func (b *Bot) Start(ctx context.Context, guild snowflake.ID, h Handler) error {
	if _, err := b.client.Rest.SetGuildCommands(b.client.ApplicationID, guild, h.Commands(), rest.WithCtx(ctx)); err != nil {
		return fmt.Errorf("discordbot: register commands: %w", err)
	}
	b.client.AddEventListeners(b.listeners(ctx, h)...)
	if err := b.client.OpenGateway(ctx); err != nil {
		return fmt.Errorf("discordbot: connect: %w", err)
	}
	return nil
}

// Ready reports whether the gateway connection is up.
func (b *Bot) Ready(context.Context) error {
	if b.client.Gateway == nil || b.client.Gateway.Status() != gateway.StatusReady {
		return errors.New("not connected to Discord")
	}
	return nil
}

// Close waits for the events being handled, for as long as ctx allows, and
// only then closes the client. The other order deadlocks: DisGo's closed
// rate limiter holds every bucket, so a handler still posting would block
// for good.
func (b *Bot) Close(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		b.work.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		b.log.Warn("closing Discord with event handlers still running")
	}
	b.closeClient(ctx)
}

// run handles one event in the background.
func (b *Bot) run(f func()) {
	b.work.Go(func() {
		// One bad event must not take every conversation down with it.
		defer func() {
			if r := recover(); r != nil {
				b.log.Error("event handler panicked", "panic", r, "stack", string(debug.Stack()))
			}
		}()
		f()
	})
}

func (b *Bot) listeners(ctx context.Context, h Handler) []disgobot.EventListener {
	return []disgobot.EventListener{
		disgobot.NewListenerFunc(func(e *events.GuildMessageCreate) {
			m := bot.Message{
				ID:        e.MessageID,
				ChannelID: e.ChannelID,
				GuildID:   e.GuildID,
				AuthorID:  e.Message.Author.ID,
				AuthorBot: e.Message.Author.Bot || e.Message.Author.System,
				Content:   e.Message.Content,
			}
			b.run(func() { h.HandleMessage(ctx, m) })
		}),
		disgobot.NewListenerFunc(func(e *events.ComponentInteractionCreate) {
			in := &componentInteraction{e: e}
			b.run(func() { h.HandleInteraction(ctx, in) })
		}),
		disgobot.NewListenerFunc(func(e *events.ModalSubmitInteractionCreate) {
			in := &modalInteraction{e: e}
			b.run(func() { h.HandleInteraction(ctx, in) })
		}),
		disgobot.NewListenerFunc(func(e *events.ApplicationCommandInteractionCreate) {
			b.onCommand(ctx, h, e)
		}),
		// An agent channel deleted or moved out of the category is
		// replaced; IntentGuilds delivers these.
		disgobot.NewListenerFunc(func(e *events.GuildChannelUpdate) {
			b.run(func() { h.HandleChannelChange(ctx, e.ChannelID) })
		}),
		disgobot.NewListenerFunc(func(e *events.GuildChannelDelete) {
			b.run(func() { h.HandleChannelChange(ctx, e.ChannelID) })
		}),
	}
}

func (b *Bot) onCommand(ctx context.Context, h Handler, e *events.ApplicationCommandInteractionCreate) {
	data, ok := e.Data.(discord.SlashCommandInteractionData)
	if !ok || data.CommandName() != bot.AskCommand {
		return
	}
	// Acknowledge within Discord's three seconds; the reply follows.
	if err := e.DeferCreateMessage(true, rest.WithCtx(ctx)); err != nil {
		b.log.Warn("defer /ask", "err", err)
		return
	}
	ask := bot.Ask{
		UserID:    e.User().ID,
		ChannelID: e.Channel().ID(),
		Agent:     data.String("agent"),
		Prompt:    data.String("prompt"),
	}
	respond := func(text string) error {
		_, err := e.Client().Rest.UpdateInteractionResponse(e.ApplicationID(), e.Token(),
			discord.NewMessageUpdate().WithContent(text), rest.WithCtx(ctx))
		return err
	}
	b.run(func() { h.HandleAsk(ctx, ask, respond) })
}
