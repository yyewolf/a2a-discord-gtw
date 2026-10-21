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
// last poll (on the very first poll it only records where to start, so
// history is not replayed), then follows up every agent's unsettled tasks.
func (g *Gateway) Poll(ctx context.Context) {
	g.retryChannels(ctx)
	g.followUp(ctx)
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
		if g.streaming.has(t.ID) {
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

// followUp re-reads every recorded task that has not settled and shows what
// changed. It is how a turn whose stream was cut (kagent restarting, an
// idle connection dropped, this process restarting) still reaches its
// thread: its reply, its final state, its questions.
func (g *Gateway) followUp(ctx context.Context) {
	recs, err := g.store.UnsettledTasks(ctx)
	if err != nil {
		g.log.Warn("list unsettled tasks", "err", err)
		return
	}
	for _, rec := range recs {
		if ctx.Err() != nil {
			return
		}
		a, ok := g.agents[rec.Agent]
		if !ok || g.streaming.has(a2a.TaskID(rec.TaskID)) {
			continue
		}
		t, err := a.Client.Task(ctx, a2a.TaskID(rec.TaskID))
		if err != nil {
			g.log.Warn("follow up task", "agent", rec.Agent, "task", rec.TaskID, "err", err)
			continue
		}
		if err := g.surface(ctx, a, t); err != nil && !errors.Is(err, errDeferred) {
			g.log.Warn("follow up task", "agent", rec.Agent, "task", rec.TaskID, "err", err)
		}
	}
}

// surface shows a task's changes since it was last shown: new reply text,
// its state, and a new pending request. It never waits for a conversation
// busy with a turn: the task is deferred to the next poll, and everything is
// read again under the lock, so what a turn just posted is not posted twice.
func (g *Gateway) surface(ctx context.Context, a *Agent, t *a2a.Task) error {
	rec, known, err := g.store.Task(ctx, string(t.ID))
	if err != nil {
		return err
	}
	if known && unchanged(rec, t) {
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
	unlock, ok := g.conv.tryLock(snowflake.ID(conv.ThreadID))
	if !ok {
		return errDeferred
	}
	defer unlock()
	if rec, _, err = g.store.Task(ctx, string(t.ID)); err != nil {
		return err
	}
	if full, err = a.Client.Task(ctx, t.ID); err != nil {
		return err
	}
	text := []rune(kagent.TaskText(full))
	if rec.PostedLen < len(text) {
		for _, c := range render.Chunk(string(text[rec.PostedLen:]), render.MessageLimit) {
			g.post(ctx, snowflake.ID(conv.ThreadID), c)
		}
	}
	g.showState(ctx, conv, full.Status.State)
	return g.settle(ctx, a, conv, full, max(len(text), rec.PostedLen))
}

// unchanged reports whether a task is as it was last shown. A task waiting
// for input is always looked at: its request may have been replaced.
func unchanged(rec store.TaskRecord, t *a2a.Task) bool {
	return rec.State == string(t.Status.State) && t.Status.State != a2a.TaskStateInputRequired
}

// conversationFor returns the conversation of a task's context, opening one
// in the agent's channel if the bot has never seen it.
func (g *Gateway) conversationFor(ctx context.Context, a *Agent, t *a2a.Task) (*store.Conversation, error) {
	bind := g.binding[a.Name]
	bind.Lock()
	defer bind.Unlock()
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
	return g.openConversation(ctx, a, taskTitle(t), originKagent, t.ContextID)
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
