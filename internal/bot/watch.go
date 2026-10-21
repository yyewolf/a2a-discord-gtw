package bot

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/disgoorg/snowflake/v2"

	"github.com/yyewolf/a2a-discord-gtw/internal/kagent"
	"github.com/yyewolf/a2a-discord-gtw/internal/render"
	"github.com/yyewolf/a2a-discord-gtw/internal/store"
)

// originKagent marks conversations the bot found rather than started.
const originKagent = "from kagent"

// errDeferred postpones a task to the next poll.
var errDeferred = errors.New("bot: deferred to the next poll")

// Watch polls the watched agents every interval until ctx is done.
func (g *Gateway) Watch(ctx context.Context) {
	t := time.NewTicker(g.cfg.Watch.Interval)
	defer t.Stop()
	for {
		g.Poll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Poll surfaces, once, what changed in each watched agent's tasks since the
// last poll. On the very first poll it only records where to start, so
// history is not replayed.
func (g *Gateway) Poll(ctx context.Context) {
	for _, name := range g.order {
		a := g.agents[name]
		if !a.Watch {
			continue
		}
		if err := g.pollAgent(ctx, a); err != nil && ctx.Err() == nil {
			g.log.Warn("watch agent", "agent", name, "err", err)
		}
	}
}

func (g *Gateway) pollAgent(ctx context.Context, a *Agent) error {
	since, ok, err := g.store.Watermark(ctx, a.Name)
	if err != nil {
		return err
	}
	if !ok {
		return g.store.SetWatermark(ctx, a.Name, time.Now())
	}
	tasks, err := a.Client.TasksSince(ctx, since)
	if err != nil {
		return err
	}
	// The A2A spec leaves the list order to the server. Oldest first, so
	// the watermark only ever passes tasks that were handled.
	tasks = slices.DeleteFunc(tasks, func(t *a2a.Task) bool { return t.Status.Timestamp == nil })
	slices.SortStableFunc(tasks, func(x, y *a2a.Task) int { return x.Status.Timestamp.Compare(*y.Status.Timestamp) })
	mark := since
	for _, t := range tasks {
		if _, live := g.streaming.Load(t.ID); live {
			mark = later(mark, *t.Status.Timestamp)
			continue
		}
		if err := g.surface(ctx, a, t); err != nil {
			// Stop here, with the watermark just before this task: the
			// filter is strictly "after", and a task sharing its timestamp
			// may not have been handled either. The next poll retries from
			// there; surfacing again what was shown is a no-op.
			if werr := g.store.SetWatermark(ctx, a.Name, t.Status.Timestamp.Add(-time.Nanosecond)); werr != nil {
				return werr
			}
			if errors.Is(err, errDeferred) {
				return nil
			}
			return fmt.Errorf("task %s: %w", t.ID, err)
		}
		mark = later(mark, *t.Status.Timestamp)
	}
	return g.store.SetWatermark(ctx, a.Name, mark)
}

// surface shows a task's changes since it was last shown: new reply text,
// its state, and a new pending request.
func (g *Gateway) surface(ctx context.Context, a *Agent, t *a2a.Task) error {
	rec, known, err := g.store.Task(ctx, string(t.ID))
	if err != nil {
		return err
	}
	if known && rec.State == string(t.Status.State) && t.Status.State != a2a.TaskStateInputRequired {
		return nil
	}
	full, err := a.Client.Task(ctx, t.ID)
	if err != nil {
		return err
	}
	conv, err := g.conversationFor(ctx, a, full)
	if err != nil {
		return err
	}
	unlock := g.conv.lock(snowflake.ID(conv.ThreadID))
	defer unlock()
	text := []rune(kagent.TaskText(full))
	if rec.PostedLen < len(text) {
		for _, c := range render.Chunk(string(text[rec.PostedLen:]), render.MessageLimit) {
			g.post(snowflake.ID(conv.ThreadID), c)
		}
	}
	g.showState(ctx, conv, true, full.Status.State)
	return g.settle(ctx, a, conv, full, len(text))
}

// conversationFor returns the conversation of a task's context, opening one
// in the agent's channel if the bot has never seen it.
func (g *Gateway) conversationFor(ctx context.Context, a *Agent, t *a2a.Task) (*store.Conversation, error) {
	conv, ok, err := g.store.ConversationByContext(ctx, a.Name, t.ContextID)
	if err != nil {
		return nil, err
	}
	if ok {
		return &conv, nil
	}
	if g.isStarting(a.Name) {
		// Possibly the task of a conversation being started right now.
		return nil, errDeferred
	}
	channel, ok := g.channelOf(a.Name)
	if !ok {
		return nil, fmt.Errorf("%s has no channel", a.Name)
	}
	c, err := g.openConversation(a, channel, taskTitle(t), originKagent)
	if err != nil {
		return nil, err
	}
	c.ContextID = t.ContextID
	if err := g.store.PutConversation(ctx, *c); err != nil {
		return nil, err
	}
	return c, nil
}

// taskTitle is the first user message of a task, which for a ScheduledRun
// is its prompt.
func taskTitle(t *a2a.Task) string {
	for _, m := range t.History {
		if m != nil && m.Role == a2a.MessageRoleUser {
			if text := kagent.PartsText(m.Parts); text != "" {
				return render.Title(text)
			}
		}
	}
	id := string(t.ID)
	if len(id) > 8 {
		id = id[:8]
	}
	return "task " + id
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
