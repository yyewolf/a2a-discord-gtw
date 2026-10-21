package bot

import (
	"context"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/yyewolf/a2a-discord-gtw/internal/render"
	"github.com/yyewolf/a2a-discord-gtw/internal/store"
)

// showState updates a conversation's status: its status line when the status
// message is the bot's, a reaction on the user's message otherwise. Failures
// are logged, not returned: the status is cosmetic and must not stop a reply.
func (g *Gateway) showState(ctx context.Context, conv *store.Conversation, persisted bool, state a2a.TaskState) {
	if conv.State == string(state) {
		return
	}
	prev := a2a.TaskState(conv.State)
	ch, msg := snowflake.ID(conv.ChannelID), snowflake.ID(conv.StatusMessageID)
	if conv.OwnStatus {
		content := render.StatusLine(conv.Agent, conv.Title, conv.Origin, state)
		upd := discord.NewMessageUpdate().WithContent(content).WithAllowedMentions(&discord.AllowedMentions{})
		if _, err := g.discord.UpdateMessage(ch, msg, upd); err != nil {
			g.log.Warn("update status line", "thread", conv.ThreadID, "err", err)
		}
	} else {
		if conv.State != "" && render.StateMark(prev) != render.StateMark(state) {
			if err := g.discord.RemoveOwnReaction(ch, msg, render.StateMark(prev)); err != nil {
				g.log.Warn("remove status reaction", "thread", conv.ThreadID, "err", err)
			}
		}
		if err := g.discord.AddReaction(ch, msg, render.StateMark(state)); err != nil {
			g.log.Warn("add status reaction", "thread", conv.ThreadID, "err", err)
		}
	}
	conv.State = string(state)
	if persisted {
		if err := g.store.SetConversationState(ctx, conv.ThreadID, conv.State); err != nil {
			g.log.Warn("record conversation state", "thread", conv.ThreadID, "err", err)
		}
	}
}
