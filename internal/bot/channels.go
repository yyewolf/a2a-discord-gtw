package bot

import (
	"context"
	"fmt"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/yyewolf/a2a-discord-gtw/internal/render"
)

const topicLimit = 1024

// EnsureChannels gives every agent its text channel in the category. A
// channel recorded earlier is reused even if it was renamed or moved; one
// that was deleted is recreated. Without a record, a channel of the agent's
// name already in the category is adopted rather than duplicated.
func (g *Gateway) EnsureChannels(ctx context.Context) error {
	var existing []discord.GuildChannel
	listed := false
	for _, name := range g.order {
		a := g.agents[name]
		id, err := g.ensureChannel(ctx, a, &existing, &listed)
		if err != nil {
			return err
		}
		g.mu.Lock()
		g.byChannel[id] = name
		g.mu.Unlock()
	}
	return nil
}

func (g *Gateway) ensureChannel(ctx context.Context, a *Agent, existing *[]discord.GuildChannel, listed *bool) (snowflake.ID, error) {
	stored, ok, err := g.store.AgentChannel(ctx, a.Name)
	if err != nil {
		return 0, err
	}
	if ok {
		_, err := g.discord.GetChannel(snowflake.ID(stored), rest.WithCtx(ctx))
		switch {
		case err == nil:
			return snowflake.ID(stored), nil
		case !isUnknownChannel(err):
			return 0, fmt.Errorf("bot: channel of %s: %w", a.Name, err)
		}
		g.log.Info("agent channel was deleted, recreating it", "agent", a.Name, "channel", stored)
	} else {
		if !*listed {
			if *existing, err = g.discord.GetGuildChannels(g.guild, rest.WithCtx(ctx)); err != nil {
				return 0, fmt.Errorf("bot: list guild channels: %w", err)
			}
			*listed = true
		}
		for _, ch := range *existing {
			if ch.Type() == discord.ChannelTypeGuildText && ch.Name() == a.Name && ch.ParentID() != nil && *ch.ParentID() == g.category {
				g.log.Info("adopting existing agent channel", "agent", a.Name, "channel", ch.ID())
				return ch.ID(), g.store.SetAgentChannel(ctx, a.Name, uint64(ch.ID()))
			}
		}
	}

	topic := ""
	if card, err := a.Client.Card(ctx); err == nil {
		topic = render.Truncate(card.Description, topicLimit)
	} else {
		g.log.Warn("could not read agent card for the channel topic", "agent", a.Name, "err", err)
	}
	ch, err := g.discord.CreateGuildChannel(g.guild, discord.GuildTextChannelCreate{
		Name:     a.Name,
		Topic:    topic,
		ParentID: g.category,
	}, rest.WithCtx(ctx))
	if err != nil {
		return 0, fmt.Errorf("bot: create channel for %s: %w", a.Name, err)
	}
	g.log.Info("created agent channel", "agent", a.Name, "channel", ch.ID())
	return ch.ID(), g.store.SetAgentChannel(ctx, a.Name, uint64(ch.ID()))
}
