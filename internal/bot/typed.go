package bot

import (
	"context"
	"fmt"
	"strings"

	"github.com/disgoorg/snowflake/v2"

	"github.com/yyewolf/a2a-discord-gtw/internal/kagent"
	"github.com/yyewolf/a2a-discord-gtw/internal/store"
)

// answerTyped uses a message typed in a thread as the answer to its pending
// question, when that is unambiguous: one question. A choice is matched
// case-insensitively, anything else is a free-text answer. Approvals and
// multi-question requests need the buttons.
func (g *Gateway) answerTyped(ctx context.Context, agent *Agent, conv *store.Conversation, m Message) {
	thread := snowflake.ID(conv.ThreadID)
	task, err := agent.Client.PendingTask(ctx, conv.ContextID)
	if err != nil {
		g.post(thread, fmt.Sprintf("⚠️ **%s** is busy with another task; try again in a moment.", agent.Name))
		return
	}
	req, err := kagent.PendingRequest(task)
	if err != nil || req.IsApproval() || len(req.Questions) != 1 {
		g.post(thread, "↑ Answer the pending request with its buttons first.")
		return
	}
	answer := matchChoice(req.Questions[0], m.Content)
	msg, err := kagent.AnswerMessage(task, req, [][]string{answer}, m.Content)
	if err != nil {
		g.post(thread, fmt.Sprintf("⚠️ %v", err))
		return
	}
	if err := g.store.ClearPartials(ctx, string(task.ID)); err != nil {
		g.log.Warn("clear partial answers", "task", task.ID, "err", err)
	}
	g.turn(ctx, agent, conv, true, msg)
}

// matchChoice maps typed text to a choice, or keeps it as free text.
func matchChoice(q kagent.Question, text string) []string {
	text = strings.TrimSpace(text)
	for _, c := range q.Choices {
		if strings.EqualFold(c, text) {
			return []string{c}
		}
	}
	return []string{text}
}
