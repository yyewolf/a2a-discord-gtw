package bot

import (
	"context"
	"errors"
	"fmt"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/yyewolf/a2a-discord-gtw/internal/render"
)

const topicLimit = 1024

// EnsureChannels gives every agent its text channel in the category. A
// channel recorded earlier is reused even if it was renamed; one that was
// deleted is recreated. Without a record, a channel of the agent's name
// already in the category is adopted rather than duplicated.
//
// One agent's failure does not stop the others: it is returned (joined)
// for the log, remembered for readiness, and retried later.
func (g *Gateway) EnsureChannels(ctx context.Context) error {
	var existing []discord.GuildChannel
	listed := false
	var errs []error
	for _, name := range g.order {
		if err := g.ensureAgent(ctx, g.agents[name], &existing, &listed); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ensureAgent provisions one agent's channel and records the outcome.
func (g *Gateway) ensureAgent(ctx context.Context, a *Agent, existing *[]discord.GuildChannel, listed *bool) error {
	id, err := g.ensureChannel(ctx, a, existing, listed)
	if err != nil {
		err = channelHint(fmt.Errorf("bot: channel of %s: %w", a.Name, err), g.category)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for ch, name := range g.byChannel {
		if name == a.Name {
			delete(g.byChannel, ch)
		}
	}
	if err == nil {
		g.byChannel[id] = a.Name
		delete(g.channelErr, a.Name)
		return nil
	}
	g.channelErr[a.Name] = err
	return err
}

// ChannelsReady fails while an agent has no channel, saying why.
func (g *Gateway) ChannelsReady(context.Context) error {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var errs []error
	for _, name := range g.order {
		if err, ok := g.channelErr[name]; ok {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// channelHint explains Discord's "Missing Permissions" on channel creation.
func channelHint(err error, category snowflake.ID) error {
	var re *rest.Error
	if errors.As(err, &re) && re.Code == rest.JSONErrorCodeLackPermissionsToPerformAction {
		return fmt.Errorf("%w (give the bot Manage Channels on category %s)", err, category)
	}
	return err
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
			return 0, err
		}
		g.log.Info("agent channel was deleted, recreating it", "agent", a.Name, "channel", stored)
	} else {
		if !*listed {
			if *existing, err = g.discord.GetGuildChannels(g.guild, rest.WithCtx(ctx)); err != nil {
				return 0, fmt.Errorf("list guild channels: %w", err)
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
		return 0, fmt.Errorf("create: %w", err)
	}
	g.log.Info("created agent channel", "agent", a.Name, "channel", ch.ID())
	return ch.ID(), g.store.SetAgentChannel(ctx, a.Name, uint64(ch.ID()))
}
