package bot

import (
	"context"
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

// AskCommand is the name of the slash command.
const AskCommand = "ask"

const (
	askAgentOption  = "agent"
	askPromptOption = "prompt"
)

// Commands are the guild slash commands the gateway serves.
func (g *Gateway) Commands() []discord.ApplicationCommandCreate {
	choices := make([]discord.ApplicationCommandOptionChoiceString, 0, len(g.order))
	for _, name := range g.order {
		choices = append(choices, discord.ApplicationCommandOptionChoiceString{Name: name, Value: name})
	}
	return []discord.ApplicationCommandCreate{discord.SlashCommandCreate{
		Name:        AskCommand,
		Description: "Start a conversation with an agent",
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionString{
				Name: askPromptOption, Description: "What to ask", Required: true,
			},
			discord.ApplicationCommandOptionString{
				Name: askAgentOption, Description: "Which agent (defaults to this channel's)", Choices: choices,
			},
		},
		Contexts: []discord.InteractionContextType{discord.InteractionContextTypeGuild},
	}}
}

// Ask is a use of /ask.
type Ask struct {
	UserID    snowflake.ID
	ChannelID snowflake.ID
	// Agent is the agent option, empty when not given.
	Agent  string
	Prompt string
}

// HandleAsk starts a conversation from /ask: a status message in the agent's
// channel, a thread on it, and the prompt sent to the agent. respond answers
// the command (ephemerally) as soon as the thread exists; HandleAsk returns
// when the agent's turn is over.
func (g *Gateway) HandleAsk(ctx context.Context, a Ask, respond func(text string) error) {
	say := func(text string) {
		if err := respond(text); err != nil {
			g.log.Warn("respond to /ask", "err", err)
		}
	}
	if !g.Allowed(a.UserID) {
		say("Only the agents' owners can talk to them.")
		return
	}
	agent, err := g.askTarget(ctx, a)
	if err != nil {
		say(err.Error())
		return
	}
	conv, err := g.openConversation(ctx, agent, render.Title(a.Prompt), mention(a.UserID), "")
	if err != nil {
		g.log.Error("open conversation", "agent", agent.Name, "err", err)
		say("⚠️ Could not open a thread: " + err.Error())
		return
	}
	say(fmt.Sprintf("Started in <#%d>.", conv.ThreadID))
	unlock := g.conv.lock(snowflake.ID(conv.ThreadID))
	defer unlock()
	g.turn(ctx, agent, conv, kagent.TextMessage("", a.Prompt))
}

// askTarget is the agent named by the option, or else the agent of the
// channel (or conversation thread) the command was used in.
func (g *Gateway) askTarget(ctx context.Context, a Ask) (*Agent, error) {
	if name := strings.TrimSpace(a.Agent); name != "" {
		if agent, ok := g.agents[name]; ok {
			return agent, nil
		}
		return nil, fmt.Errorf("there is no agent called **%s**", name)
	}
	if agent, ok := g.agentForChannel(a.ChannelID); ok {
		return agent, nil
	}
	if conv, ok, err := g.store.ConversationByThread(ctx, uint64(a.ChannelID)); err == nil && ok {
		if agent, ok := g.agents[conv.Agent]; ok {
			return agent, nil
		}
	}
	return nil, fmt.Errorf("outside an agent's channel, pick one with the `%s` option", askAgentOption)
}

// openConversation posts a status message in the agent's channel, opens a
// thread on it and stores the conversation; contextID may be empty until
// the first turn names it. A channel found deleted is provisioned again and
// the post retried.
func (g *Gateway) openConversation(ctx context.Context, agent *Agent, title, origin, contextID string) (*store.Conversation, error) {
	state := a2a.TaskStateSubmitted
	post := discord.NewMessageCreate().
		WithContent(render.StatusLine(agent.Name, title, origin, state)).
		WithAllowedMentions(&discord.AllowedMentions{})
	channel, err := g.agentChannel(ctx, agent.Name)
	if err != nil {
		return nil, err
	}
	status, err := g.discord.CreateMessage(channel, post, rest.WithCtx(ctx))
	if isUnknownChannel(err) {
		if channel, err = g.reprovision(ctx, agent); err != nil {
			return nil, err
		}
		status, err = g.discord.CreateMessage(channel, post, rest.WithCtx(ctx))
	}
	if err != nil {
		return nil, err
	}
	th, err := g.discord.CreateThreadFromMessage(channel, status.ID, discord.ThreadCreateFromMessage{Name: render.ThreadName(title)}, rest.WithCtx(ctx))
	if err != nil {
		return nil, err
	}
	conv := &store.Conversation{
		ThreadID:        uint64(th.ID()),
		Agent:           agent.Name,
		ContextID:       contextID,
		ChannelID:       uint64(channel),
		StatusMessageID: uint64(status.ID),
		Title:           title,
		OwnStatus:       true,
		State:           string(state),
		Origin:          origin,
	}
	if err := g.store.PutConversation(ctx, *conv); err != nil {
		// Do not leave a thread nothing will ever answer in.
		if derr := g.discord.DeleteChannel(th.ID(), rest.WithCtx(ctx)); derr != nil {
			g.log.Warn("remove orphan thread", "thread", th.ID(), "err", derr)
		}
		if derr := g.discord.DeleteMessage(channel, status.ID, rest.WithCtx(ctx)); derr != nil {
			g.log.Warn("remove orphan status message", "message", status.ID, "err", derr)
		}
		return nil, err
	}
	return conv, nil
}
