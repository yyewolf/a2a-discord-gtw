// Package discordbot connects the gateway to Discord with DisGo: it opens the
// gateway connection, registers the slash commands, and turns DisGo events
// into calls on the gateway.
package discordbot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	Commands() []discord.ApplicationCommandCreate
}

// Bot is a DisGo client bound to a Handler. Each event is handled in its own
// goroutine, since a handler returns only when the agent's turn is over.
type Bot struct {
	client *disgobot.Client
	log    *slog.Logger

	mu      sync.Mutex
	handler Handler
	ctx     context.Context
	work    sync.WaitGroup
}

// New builds the client. It does not connect.
func New(token string, log *slog.Logger) (*Bot, error) {
	b := &Bot{log: log}
	client, err := disgo.New(token,
		disgobot.WithLogger(log.With("component", "disgo")),
		disgobot.WithGatewayConfigOpts(gateway.WithIntents(
			gateway.IntentGuilds,
			gateway.IntentGuildMessages,
			gateway.IntentMessageContent,
		)),
		// Everything the gateway needs comes with the events or from its
		// own store; nothing is read from DisGo's caches.
		disgobot.WithCacheConfigOpts(cache.WithCaches(cache.FlagsNone)),
		disgobot.WithEventListenerFunc(b.onMessage),
		disgobot.WithEventListenerFunc(b.onComponent),
		disgobot.WithEventListenerFunc(b.onModal),
		disgobot.WithEventListenerFunc(b.onCommand),
	)
	if err != nil {
		return nil, fmt.Errorf("discordbot: %w", err)
	}
	b.client = client
	return b, nil
}

// Rest is the REST client, for the gateway.
func (b *Bot) Rest() rest.Rest { return b.client.Rest }

// Start registers the handler's commands in the guild and connects. Events
// are handled under ctx until Close.
func (b *Bot) Start(ctx context.Context, guild snowflake.ID, h Handler) error {
	b.mu.Lock()
	b.handler, b.ctx = h, ctx
	b.mu.Unlock()
	if _, err := b.client.Rest.SetGuildCommands(b.client.ApplicationID, guild, h.Commands()); err != nil {
		return fmt.Errorf("discordbot: register commands: %w", err)
	}
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

// Close disconnects, then waits for the events being handled; their context
// should be cancelled first so they stop promptly.
func (b *Bot) Close(ctx context.Context) {
	b.client.Close(ctx)
	b.work.Wait()
}

// handle runs f in the background with the context given to Start.
func (b *Bot) handle(f func(ctx context.Context, h Handler)) {
	b.mu.Lock()
	h, ctx := b.handler, b.ctx
	b.mu.Unlock()
	if h == nil {
		return
	}
	b.work.Go(func() { f(ctx, h) })
}

func (b *Bot) onMessage(e *events.GuildMessageCreate) {
	m := bot.Message{
		ID:        e.MessageID,
		ChannelID: e.ChannelID,
		GuildID:   e.GuildID,
		AuthorID:  e.Message.Author.ID,
		AuthorBot: e.Message.Author.Bot || e.Message.Author.System,
		Content:   e.Message.Content,
	}
	b.handle(func(ctx context.Context, h Handler) { h.HandleMessage(ctx, m) })
}

func (b *Bot) onComponent(e *events.ComponentInteractionCreate) {
	in := &componentInteraction{e: e}
	b.handle(func(ctx context.Context, h Handler) { h.HandleInteraction(ctx, in) })
}

func (b *Bot) onModal(e *events.ModalSubmitInteractionCreate) {
	in := &modalInteraction{e: e}
	b.handle(func(ctx context.Context, h Handler) { h.HandleInteraction(ctx, in) })
}

func (b *Bot) onCommand(e *events.ApplicationCommandInteractionCreate) {
	data, ok := e.Data.(discord.SlashCommandInteractionData)
	if !ok || data.CommandName() != bot.AskCommand {
		return
	}
	// Acknowledge within Discord's three seconds; the reply follows.
	if err := e.DeferCreateMessage(true); err != nil {
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
			discord.NewMessageUpdate().WithContent(text))
		return err
	}
	b.handle(func(ctx context.Context, h Handler) { h.HandleAsk(ctx, ask, respond) })
}
