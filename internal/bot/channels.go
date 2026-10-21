package bot

import (
	"context"
	"errors"
	"fmt"
	"time"

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
		delete(g.channelRetry, a.Name)
		return nil
	}
	g.channelErr[a.Name] = err
	wait := min(max(2*g.channelRetry[a.Name].wait, firstChannelRetry), maxChannelRetry)
	g.channelRetry[a.Name] = channelRetry{next: time.Now().Add(wait), wait: wait}
	return err
}

const (
	firstChannelRetry = 30 * time.Second
	maxChannelRetry   = 10 * time.Minute
)

// channelRetry is when an agent's missing channel is tried again.
type channelRetry struct {
	next time.Time
	wait time.Duration
}

// retryChannels tries again the missing channels whose backoff has passed.
func (g *Gateway) retryChannels(ctx context.Context) {
	var existing []discord.GuildChannel
	listed := false
	for _, name := range g.order {
		g.mu.RLock()
		r, missing := g.channelRetry[name]
		g.mu.RUnlock()
		if !missing || time.Now().Before(r.next) {
			continue
		}
		if err := g.ensureAgent(ctx, g.agents[name], &existing, &listed); err != nil {
			g.log.Warn("agent channel still missing", "agent", name, "err", err)
		} else {
			g.log.Info("agent channel provisioned", "agent", name)
		}
	}
}

// agentChannel is an agent's channel, provisioned on the spot if it is
// missing, whatever the backoff: someone is waiting for it.
func (g *Gateway) agentChannel(ctx context.Context, agent string) (snowflake.ID, error) {
	if ch, ok := g.channelOf(agent); ok {
		return ch, nil
	}
	return g.reprovision(ctx, g.agents[agent])
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

// inCategory reports whether a channel is in the agents' category; channels
// outside it are not the gateway's.
func (g *Gateway) inCategory(ch discord.Channel) bool {
	gc, ok := ch.(discord.GuildChannel)
	return ok && gc.ParentID() != nil && *gc.ParentID() == g.category
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
		ch, err := g.discord.GetChannel(snowflake.ID(stored), rest.WithCtx(ctx))
		switch {
		case err == nil && g.inCategory(ch):
			return snowflake.ID(stored), nil
		case err == nil:
			g.log.Info("agent channel was moved out of the category, replacing it", "agent", a.Name, "channel", stored)
		case isUnknownChannel(err):
			g.log.Info("agent channel was deleted, recreating it", "agent", a.Name, "channel", stored)
		default:
			return 0, err
		}
	}
	// No usable record: adopt a channel of the agent's name in the
	// category before creating one, so nothing is duplicated.
	if !*listed {
		if *existing, err = g.discord.GetGuildChannels(g.guild, rest.WithCtx(ctx)); err != nil {
			return 0, fmt.Errorf("list guild channels: %w", err)
		}
		*listed = true
	}
	for _, ch := range *existing {
		if ch.Type() == discord.ChannelTypeGuildText && ch.Name() == a.Name && ch.ID() != snowflake.ID(stored) && g.inCategory(ch) {
			g.log.Info("adopting existing agent channel", "agent", a.Name, "channel", ch.ID())
			return ch.ID(), g.store.SetAgentChannel(ctx, a.Name, uint64(ch.ID()))
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

// reprovision checks an agent's channel again, now, and returns the one it
// ends up with.
func (g *Gateway) reprovision(ctx context.Context, a *Agent) (snowflake.ID, error) {
	var existing []discord.GuildChannel
	listed := false
	if err := g.ensureAgent(ctx, a, &existing, &listed); err != nil {
		return 0, err
	}
	ch, _ := g.channelOf(a.Name)
	return ch, nil
}

// HandleChannelChange reacts to an agent channel being deleted or edited:
// one deleted or moved out of the category is replaced. Other channels are
// ignored.
func (g *Gateway) HandleChannelChange(ctx context.Context, channel snowflake.ID) {
	agent, ok := g.agentForChannel(channel)
	if !ok {
		return
	}
	if _, err := g.reprovision(ctx, agent); err != nil {
		g.log.Error("agent channel changed and could not be replaced", "agent", agent.Name, "err", err)
	}
}
