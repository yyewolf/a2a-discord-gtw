package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/yyewolf/a2a-discord-gtw/internal/kagent"
	"github.com/yyewolf/a2a-discord-gtw/internal/render"
	"github.com/yyewolf/a2a-discord-gtw/internal/store"
)

// Message is a Discord message the gateway may act on.
type Message struct {
	ID        snowflake.ID
	ChannelID snowflake.ID
	GuildID   snowflake.ID
	AuthorID  snowflake.ID
	AuthorBot bool
	Content   string
}

// HandleMessage routes a message: in an agent's channel it starts a
// conversation threaded on the message; in a conversation's thread it
// continues it. Everything else, and everyone outside the allowlist, is
// ignored. It returns when the agent's turn is over.
func (g *Gateway) HandleMessage(ctx context.Context, m Message) {
	if m.AuthorBot || m.GuildID != g.guild || !g.Allowed(m.AuthorID) || strings.TrimSpace(m.Content) == "" {
		return
	}
	if agent, ok := g.agentForChannel(m.ChannelID); ok {
		g.startFromMessage(ctx, agent, m)
		return
	}
	conv, ok, err := g.store.ConversationByThread(ctx, uint64(m.ChannelID))
	if err != nil {
		g.log.Error("look up thread", "thread", m.ChannelID, "err", err)
		return
	}
	if ok {
		g.reply(ctx, conv, m)
	}
}

func (g *Gateway) startFromMessage(ctx context.Context, agent *Agent, m Message) {
	title := render.Title(m.Content)
	th, err := g.discord.CreateThreadFromMessage(m.ChannelID, m.ID, discord.ThreadCreateFromMessage{Name: render.ThreadName(title)}, rest.WithCtx(ctx))
	if err != nil {
		g.log.Error("open thread", "channel", m.ChannelID, "message", m.ID, "err", err)
		return
	}
	conv := &store.Conversation{
		ThreadID:        uint64(th.ID()),
		Agent:           agent.Name,
		ChannelID:       uint64(m.ChannelID),
		StatusMessageID: uint64(m.ID),
		Title:           title,
		Origin:          mention(m.AuthorID),
	}
	unlock := g.conv.lock(th.ID())
	defer unlock()
	g.turn(ctx, agent, conv, false, kagent.TextMessage("", m.Content))
}

func (g *Gateway) reply(ctx context.Context, conv store.Conversation, m Message) {
	agent, ok := g.agents[conv.Agent]
	if !ok {
		g.post(ctx, m.ChannelID, fmt.Sprintf("⚠️ **%s** is no longer configured.", conv.Agent))
		return
	}
	unlock := g.conv.lock(m.ChannelID)
	defer unlock()
	err := g.run(ctx, agent, &conv, true, agent.Client.Stream(ctx, kagent.TextMessage(conv.ContextID, m.Content)))
	if errors.Is(err, errWaiting) {
		g.answerTyped(ctx, agent, &conv, m)
		return
	}
	if err != nil {
		g.log.Warn("turn ended with an error", "agent", agent.Name, "thread", conv.ThreadID, "err", err)
	}
}

// turn sends a message and relays the reply.
func (g *Gateway) turn(ctx context.Context, agent *Agent, conv *store.Conversation, persisted bool, msg *a2a.Message) {
	if err := g.run(ctx, agent, conv, persisted, agent.Client.Stream(ctx, msg)); err != nil {
		g.log.Warn("turn ended with an error", "agent", agent.Name, "thread", conv.ThreadID, "err", err)
	}
}

func mention(user snowflake.ID) string { return "<@" + user.String() + ">" }
