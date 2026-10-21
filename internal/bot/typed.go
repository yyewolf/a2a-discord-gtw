package bot

import (
	"context"
	"errors"
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
	switch {
	case errors.Is(err, kagent.ErrNoRequest):
		// kagent refused the turn, yet nothing waits for input: another
		// task of the conversation is still running.
		g.post(ctx, thread, fmt.Sprintf("⏳ **%s** is still working on something in this conversation; try again when it is done.", agent.Name))
		return
	case err != nil:
		g.post(ctx, thread, fmt.Sprintf("⚠️ Could not reach **%s**: %v", agent.Name, err))
		return
	}
	req, err := kagent.PendingRequest(task)
	if err != nil {
		g.post(ctx, thread, fmt.Sprintf("⚠️ **%s** is waiting for input I cannot show: %v", agent.Name, err))
		return
	}
	if req.IsApproval() || len(req.Questions) != 1 {
		// Not answerable by text. Post the buttons again here: the earlier
		// ones may be far up, or may never have been posted if the turn
		// that asked was cut.
		g.post(ctx, thread, "This needs the buttons:")
		if err := g.ask(ctx, agent, conv, task, req); err != nil {
			g.log.Error("post request", "task", task.ID, "err", err)
		}
		return
	}
	answer := matchChoice(req.Questions[0], m.Content)
	msg, err := kagent.AnswerMessage(task, req, [][]string{answer}, m.Content)
	if err != nil {
		g.post(ctx, thread, fmt.Sprintf("⚠️ %v", err))
		return
	}
	if err := g.store.ClearPartials(ctx, string(task.ID)); err != nil {
		g.log.Warn("clear partial answers", "task", task.ID, "err", err)
	}
	g.turn(ctx, agent, conv, msg)
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
