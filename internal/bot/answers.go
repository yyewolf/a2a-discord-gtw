package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/yyewolf/a2a-discord-gtw/internal/kagent"
	"github.com/yyewolf/a2a-discord-gtw/internal/render"
)

// Interaction is a component use or a modal submit, abstracted from DisGo.
type Interaction interface {
	UserID() snowflake.ID
	CustomID() string
	// Values are the picks of a select menu.
	Values() []string
	// Text is the free text of a submitted modal.
	Text() string
	// Message is the message carrying the component, if any.
	Message() (*discord.Message, bool)
	// Acknowledge defers the response; the message is updated afterwards.
	Acknowledge() error
	Modal(m discord.ModalCreate) error
	Ephemeral(text string) error
}

// HandleInteraction answers a pending request from a button, select menu or
// modal. It returns when the resumed turn is over. Components not made by the
// gateway are ignored.
func (g *Gateway) HandleInteraction(ctx context.Context, in Interaction) {
	id, err := render.ParseID(in.CustomID())
	if err != nil {
		return
	}
	if !g.Allowed(in.UserID()) {
		g.ephemeral(in, "Only the agents' owners can answer them.")
		return
	}
	rec, ok, err := g.store.Task(ctx, id.Task)
	if err != nil || !ok {
		g.ephemeral(in, "I do not know this task any more.")
		return
	}
	agent, ok := g.agents[rec.Agent]
	if !ok {
		g.ephemeral(in, fmt.Sprintf("**%s** is no longer configured.", rec.Agent))
		return
	}
	switch id.Kind {
	case render.KindOther, render.KindReject, render.KindRejectAll:
		g.openModal(ctx, agent, id, in)
		return
	}
	if err := in.Acknowledge(); err != nil {
		g.log.Warn("acknowledge interaction", "err", err)
	}
	task, req, ok := g.pending(ctx, agent, id, in)
	if !ok {
		return
	}
	summary, err := g.record(ctx, id, in, req)
	if err != nil {
		g.ephemeral(in, "⚠️ "+err.Error())
		return
	}
	msg, done, err := g.collect(ctx, task, req)
	if err != nil {
		g.ephemeral(in, "⚠️ "+err.Error())
		return
	}
	if !done {
		g.markAnswered(in, summary, "waiting for the other answers")
		return
	}
	g.markAnswered(in, summary, "")
	conv, ok, err := g.store.ConversationByContext(ctx, agent.Name, task.ContextID)
	if err != nil || !ok {
		g.log.Error("conversation of an answered task", "task", task.ID, "err", err)
		return
	}
	unlock := g.conv.lock(snowflake.ID(conv.ThreadID))
	defer unlock()
	g.turn(ctx, agent, &conv, true, msg)
}

// pending fetches the task and checks the component still belongs to its
// pending request; if not, the message's components are removed.
func (g *Gateway) pending(ctx context.Context, agent *Agent, id render.ID, in Interaction) (*a2a.Task, *kagent.Request, bool) {
	task, err := agent.Client.Task(ctx, a2a.TaskID(id.Task))
	if err != nil {
		g.ephemeral(in, "⚠️ "+err.Error())
		return nil, nil, false
	}
	req, err := kagent.PendingRequest(task)
	if err != nil || !id.Matches(req.Key()) {
		g.markAnswered(in, "no longer pending", "")
		return nil, nil, false
	}
	return task, req, true
}

func (g *Gateway) openModal(ctx context.Context, agent *Agent, id render.ID, in Interaction) {
	_, req, ok := g.pending(ctx, agent, id, in)
	if !ok {
		return
	}
	submit := id
	var modal discord.ModalCreate
	switch id.Kind {
	case render.KindOther:
		if id.Index >= len(req.Questions) {
			return
		}
		submit.Kind = render.KindOtherSubmit
		modal = render.AnswerModal(submit, req.Questions[id.Index].Question)
	case render.KindReject:
		if id.Index >= len(req.Tools) {
			return
		}
		submit.Kind = render.KindRejectSubmit
		modal = render.RejectModal(submit, req.Tools[id.Index].Name)
	default:
		submit.Kind = render.KindRejectAllDone
		modal = render.RejectModal(submit, "all")
	}
	if err := in.Modal(modal); err != nil {
		g.log.Warn("open modal", "err", err)
	}
}

// record stores the answer carried by one interaction and describes it.
func (g *Gateway) record(ctx context.Context, id render.ID, in Interaction, req *kagent.Request) (string, error) {
	set := func(idx int, v any) error { return g.store.SetPartial(ctx, id.Task, req.Key(), idx, v) }
	question := func() (kagent.Question, error) {
		if id.Index >= len(req.Questions) {
			return kagent.Question{}, fmt.Errorf("question %d does not exist", id.Index+1)
		}
		return req.Questions[id.Index], nil
	}
	tool := func() (kagent.ToolCall, error) {
		if id.Index >= len(req.Tools) {
			return kagent.ToolCall{}, fmt.Errorf("tool %d does not exist", id.Index+1)
		}
		return req.Tools[id.Index], nil
	}
	switch id.Kind {
	case render.KindChoice:
		q, err := question()
		if err != nil || id.Arg >= len(q.Choices) {
			return "", fmt.Errorf("that choice does not exist")
		}
		return bold(q.Choices[id.Arg]), set(id.Index, []string{q.Choices[id.Arg]})
	case render.KindSelect:
		q, err := question()
		if err != nil {
			return "", err
		}
		var picks []string
		for _, v := range in.Values() {
			i, err := strconv.Atoi(v)
			if err != nil || i < 0 || i >= len(q.Choices) {
				return "", fmt.Errorf("that choice does not exist")
			}
			picks = append(picks, q.Choices[i])
		}
		return bold(strings.Join(picks, ", ")), set(id.Index, picks)
	case render.KindOtherSubmit:
		if _, err := question(); err != nil {
			return "", err
		}
		text := strings.TrimSpace(in.Text())
		if text == "" {
			return "", fmt.Errorf("the answer is empty")
		}
		return bold(render.Truncate(text, 200)), set(id.Index, []string{text})
	case render.KindApprove, render.KindRejectSubmit:
		tc, err := tool()
		if err != nil {
			return "", err
		}
		a := kagent.Approval{ID: tc.ID, Approved: id.Kind == render.KindApprove, RejectionReason: strings.TrimSpace(in.Text())}
		return decision(tc.Name, a), set(id.Index, a)
	case render.KindApproveAll, render.KindRejectAllDone:
		approved := id.Kind == render.KindApproveAll
		for i, tc := range req.Tools {
			a := kagent.Approval{ID: tc.ID, Approved: approved, RejectionReason: strings.TrimSpace(in.Text())}
			if err := set(i, a); err != nil {
				return "", err
			}
		}
		return decision("everything", kagent.Approval{Approved: approved, RejectionReason: strings.TrimSpace(in.Text())}), nil
	}
	return "", fmt.Errorf("unknown component")
}

// collect builds the answer message once every part of the request has been
// answered.
func (g *Gateway) collect(ctx context.Context, task *a2a.Task, req *kagent.Request) (*a2a.Message, bool, error) {
	parts, err := g.store.Partials(ctx, string(task.ID), req.Key())
	if err != nil {
		return nil, false, err
	}
	var msg *a2a.Message
	if req.IsApproval() {
		if len(parts) < len(req.Tools) {
			return nil, false, nil
		}
		approvals := make([]kagent.Approval, len(req.Tools))
		for i := range req.Tools {
			if err := json.Unmarshal(parts[i], &approvals[i]); err != nil {
				return nil, false, err
			}
		}
		msg, err = kagent.ApprovalMessage(task, req, approvals, "")
	} else {
		if len(parts) < len(req.Questions) {
			return nil, false, nil
		}
		answers := make([][]string, len(req.Questions))
		for i := range req.Questions {
			if err := json.Unmarshal(parts[i], &answers[i]); err != nil {
				return nil, false, err
			}
		}
		msg, err = kagent.AnswerMessage(task, req, answers, "")
	}
	if err != nil {
		return nil, false, err
	}
	if err := g.store.ClearPartials(ctx, string(task.ID)); err != nil {
		g.log.Warn("clear partial answers", "task", task.ID, "err", err)
	}
	return msg, true, nil
}

// markAnswered replaces a request message's components with what was chosen.
func (g *Gateway) markAnswered(in Interaction, summary, note string) {
	m, ok := in.Message()
	if !ok {
		return
	}
	if _, err := g.discord.UpdateMessage(m.ChannelID, m.ID, render.Answered(m.Content, summary, in.UserID(), note)); err != nil {
		g.log.Warn("mark request answered", "message", m.ID, "err", err)
	}
}

func (g *Gateway) ephemeral(in Interaction, text string) {
	if err := in.Ephemeral(text); err != nil {
		g.log.Warn("ephemeral reply", "err", err)
	}
}

func bold(s string) string { return "**" + s + "**" }

func decision(what string, a kagent.Approval) string {
	if a.Approved {
		return "approved " + bold(what)
	}
	if a.RejectionReason != "" {
		return "rejected " + bold(what) + ": " + render.Truncate(a.RejectionReason, 200)
	}
	return "rejected " + bold(what)
}
