package bot

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/yyewolf/a2a-discord-gtw/internal/kagent"
	"github.com/yyewolf/a2a-discord-gtw/internal/render"
	"github.com/yyewolf/a2a-discord-gtw/internal/store"
)

const (
	// editInterval spaces out edits of a streaming reply; Discord allows
	// about five edits per five seconds per channel.
	editInterval = 1500 * time.Millisecond
	// typingInterval renews the typing indicator, which lasts ten seconds.
	typingInterval = 8 * time.Second
)

// relay follows one turn of a conversation: it streams the agent's reply into
// the thread, keeps the status current, and asks the pending question when
// the task pauses.
type relay struct {
	g     *Gateway
	agent *Agent
	conv  *store.Conversation
	// started ends the agent's "starting" mark once the context is known.
	started func()

	task *a2a.Task
	seen []a2a.TaskID // every task of the turn, released from streaming at the end
	// rec is the record of the task being streamed; base is how much of its
	// reply earlier turns posted, so rec.PostedLen = base + this turn's text.
	rec        store.TaskRecord
	base       int
	transcript kagent.Transcript
	msgs       []snowflake.ID
	posted     []string
	lastEdit   time.Time
}

// errWaiting is returned by run when kagent refused the turn because the
// conversation has a question pending.
var errWaiting = errors.New("bot: conversation is waiting for an answer")

// errIdle ends a stream that stayed silent too long. The task goes on in
// kagent and is followed up by polling.
var errIdle = errors.New("bot: no event from the agent for too long")

// run consumes the event stream of one turn of a stored conversation. A
// conversation without a context yet is bound to the one the stream reveals.
func (g *Gateway) run(ctx context.Context, agent *Agent, conv *store.Conversation, msg *a2a.Message) error {
	r := &relay{g: g, agent: agent, conv: conv, started: func() {}}
	if conv.ContextID == "" {
		r.started = g.beginStart(agent.Name)
		defer r.started()
	}
	typingCtx, stopTyping := context.WithCancel(ctx)
	typing := make(chan struct{})
	go func() {
		defer close(typing)
		r.typing(typingCtx)
	}()
	err := r.consume(ctx, msg)
	stopTyping()
	<-typing
	if errors.Is(err, errWaiting) {
		return err
	}
	return r.finish(ctx, err)
}

func (r *relay) typing(ctx context.Context) {
	t := time.NewTicker(typingInterval)
	defer t.Stop()
	for {
		// Typing failures are harmless; the next tick retries.
		_ = r.g.discord.SendTyping(snowflake.ID(r.conv.ThreadID), rest.WithCtx(ctx))
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// streamItem is one element of an event stream.
type streamItem struct {
	ev  a2a.Event
	err error
}

// consume sends msg and follows the stream. The stream is read by its own
// goroutine, so the relay can also wake on a timer: to show text that a
// throttled edit held back while the agent goes quiet.
func (r *relay) consume(ctx context.Context, msg *a2a.Message) error {
	sctx, cancel := context.WithCancel(ctx)
	items := make(chan streamItem)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(items)
		for ev, err := range r.agent.Client.Stream(sctx, msg) {
			select {
			case items <- streamItem{ev, err}:
			case <-sctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	defer func() {
		cancel()
		<-done
	}()

	flushTimer := time.NewTimer(time.Hour)
	flushTimer.Stop()
	defer flushTimer.Stop()
	idle := time.NewTimer(r.g.idle)
	defer idle.Stop()
	first := true
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-idle.C:
			return errIdle
		case <-flushTimer.C:
			r.flush(ctx, false)
		case it, ok := <-items:
			if !ok {
				return nil
			}
			idle.Reset(r.g.idle)
			if it.err != nil {
				if first && kagent.IsWaitingForInput(it.err) {
					return errWaiting
				}
				return it.err
			}
			first = false
			if err := r.handle(ctx, it.ev); err != nil {
				return err
			}
			if r.dirty() {
				// Trailing edge: show the held-back text once the edit
				// interval has passed, even if no other event comes.
				flushTimer.Reset(max(time.Until(r.lastEdit.Add(editInterval)), 0))
			}
		}
	}
}

// handle applies one event of the stream.
func (r *relay) handle(ctx context.Context, ev a2a.Event) error {
	if err := r.observe(ctx, ev); err != nil {
		return err
	}
	switch e := ev.(type) {
	case *a2a.Task:
		r.task = e
	case *a2a.TaskStatusUpdateEvent:
		if r.task == nil {
			r.task = &a2a.Task{ID: e.TaskID, ContextID: e.ContextID}
		}
		r.task.Status = e.Status
	case *a2a.TaskArtifactUpdateEvent:
		r.transcript.Add(e)
		r.flush(ctx, false)
	}
	if r.task != nil {
		r.g.showState(ctx, r.conv, r.task.Status.State)
	}
	return nil
}

// dirty reports text not shown yet.
func (r *relay) dirty() bool {
	return strings.Join(r.posted, "\n") != strings.Join(render.Chunk(r.transcript.Text(), render.MessageLimit), "\n")
}

// observe binds the conversation to its context and marks the task as
// streamed, the first time each is seen.
func (r *relay) observe(ctx context.Context, ev a2a.Event) error {
	info := ev.TaskInfo()
	if r.conv.ContextID == "" && info.ContextID != "" {
		bind := r.g.binding[r.agent.Name]
		bind.Lock()
		err := r.g.store.SetConversationContext(ctx, r.conv.ThreadID, info.ContextID)
		if err == nil {
			r.conv.ContextID = info.ContextID
			r.started()
		}
		bind.Unlock()
		if err != nil {
			return err
		}
	}
	if info.TaskID != "" && !slices.Contains(r.seen, info.TaskID) {
		r.seen = append(r.seen, info.TaskID)
		r.g.streaming.add(info.TaskID)
		if err := r.track(ctx, info); err != nil {
			return err
		}
	}
	return nil
}

// track records a task the moment it appears, so that if the stream is cut
// it is known and followed up (see Gateway.followUp), and continues the
// count of its reply already posted in earlier turns.
func (r *relay) track(ctx context.Context, info a2a.TaskInfo) error {
	rec, ok, err := r.g.store.Task(ctx, string(info.TaskID))
	if err != nil {
		return err
	}
	if !ok {
		rec = store.TaskRecord{TaskID: string(info.TaskID), Agent: r.agent.Name, ContextID: info.ContextID, State: string(a2a.TaskStateWorking)}
	}
	r.rec, r.base = rec, rec.PostedLen
	return r.g.store.PutTask(ctx, r.rec)
}

// flush brings the thread's reply messages in line with the transcript,
// throttled unless final.
func (r *relay) flush(ctx context.Context, final bool) {
	if !final && time.Since(r.lastEdit) < editInterval {
		return
	}
	chunks := render.Chunk(r.transcript.Text(), render.MessageLimit)
	thread := snowflake.ID(r.conv.ThreadID)
	for i, c := range chunks {
		switch {
		case i < len(r.msgs) && r.posted[i] == c:
			continue
		case i < len(r.msgs):
			upd := discord.NewMessageUpdate().WithContent(c).WithAllowedMentions(&discord.AllowedMentions{})
			if _, err := r.g.discord.UpdateMessage(thread, r.msgs[i], upd, rest.WithCtx(ctx)); err != nil {
				r.g.log.Warn("edit reply", "thread", thread, "err", err)
				continue
			}
		default:
			m, err := r.g.discord.CreateMessage(thread, discord.NewMessageCreate().WithContent(c).WithAllowedMentions(&discord.AllowedMentions{}), rest.WithCtx(ctx))
			if err != nil {
				r.g.log.Warn("post reply", "thread", thread, "err", err)
				return
			}
			r.msgs = append(r.msgs, m.ID)
			r.posted = append(r.posted, "")
		}
		r.posted[i] = c
	}
	r.lastEdit = time.Now()
	if r.rec.TaskID != "" {
		r.rec.PostedLen = r.base + len([]rune(r.transcript.Text()))
		if err := r.g.store.PutTask(ctx, r.rec); err != nil {
			r.g.log.Warn("record posted text", "task", r.rec.TaskID, "err", err)
		}
	}
}

// finish posts the rest of the reply, reports a failure, and asks the
// pending question if the task paused.
func (r *relay) finish(ctx context.Context, streamErr error) error {
	r.flush(ctx, true)
	defer func() {
		for _, id := range r.seen {
			r.g.streaming.remove(id)
		}
	}()
	if errors.Is(streamErr, errIdle) {
		r.g.post(ctx, snowflake.ID(r.conv.ThreadID), fmt.Sprintf("⏳ **%s** has been quiet for %s; I will post the result here when it is done.", r.agent.Name, r.g.idle))
		return nil
	}
	if streamErr != nil {
		// On shutdown the stream ends because ctx did; nothing was lost,
		// and nothing can be posted any more.
		if ctx.Err() == nil {
			r.g.post(ctx, snowflake.ID(r.conv.ThreadID), fmt.Sprintf("⚠️ Lost the connection to **%s**: %v", r.agent.Name, streamErr))
		}
		return streamErr
	}
	if r.task == nil {
		return errors.New("bot: the agent returned no task")
	}
	// The stream's last task may lack artifacts or the request payload;
	// kagent's stored task is authoritative.
	full, err := r.agent.Client.Task(ctx, r.task.ID)
	if err != nil {
		r.g.log.Warn("read final task", "task", r.task.ID, "err", err)
		full = r.task
	}
	if r.transcript.Text() == "" && full.Status.State != a2a.TaskStateInputRequired {
		// Nothing streamed: show the stored reply, or the failure reason,
		// minus what earlier turns of the task already posted.
		if text := []rune(kagent.TaskText(full)); r.base < len(text) {
			r.transcript.Add(&a2a.TaskArtifactUpdateEvent{Artifact: &a2a.Artifact{ID: "final", Parts: a2a.ContentParts{a2a.NewTextPart(string(text[r.base:]))}}})
			r.flush(ctx, true)
		}
	}
	r.g.showState(ctx, r.conv, full.Status.State)
	return r.g.settle(ctx, r.agent, r.conv, full, len([]rune(kagent.TaskText(full))))
}

// settle records what has been shown for a task and, if it waits for input,
// asks for it once.
func (g *Gateway) settle(ctx context.Context, agent *Agent, conv *store.Conversation, t *a2a.Task, postedLen int) error {
	rec, _, err := g.store.Task(ctx, string(t.ID))
	if err != nil {
		return err
	}
	rec.TaskID, rec.Agent, rec.ContextID = string(t.ID), agent.Name, t.ContextID
	rec.State, rec.PostedLen = string(t.Status.State), postedLen
	if req, err := kagent.PendingRequest(t); err == nil {
		if rec.RequestKey != req.Key() {
			// Recorded as asked only once posted: a failure is retried by
			// the next follow-up.
			if err := g.ask(ctx, agent, conv, t, req); err != nil {
				g.log.Error("post request", "thread", conv.ThreadID, "task", t.ID, "err", err)
			} else {
				rec.RequestKey = req.Key()
			}
		}
	} else if !errors.Is(err, kagent.ErrNoRequest) {
		g.post(ctx, snowflake.ID(conv.ThreadID), fmt.Sprintf("⚠️ **%s** is waiting for input I cannot show: %v", agent.Name, err))
	}
	return g.store.PutTask(ctx, rec)
}

// ask posts a pending request in the conversation's thread.
func (g *Gateway) ask(ctx context.Context, agent *Agent, conv *store.Conversation, t *a2a.Task, req *kagent.Request) error {
	thread := snowflake.ID(conv.ThreadID)
	var msgs []discord.MessageCreate
	if req.IsApproval() {
		msgs = []discord.MessageCreate{render.ApprovalMessage(agent.Name, string(t.ID), req, g.notify)}
	} else {
		msgs = render.QuestionMessages(agent.Name, string(t.ID), req, g.notify)
	}
	for _, m := range msgs {
		if _, err := g.discord.CreateMessage(thread, m, rest.WithCtx(ctx)); err != nil {
			return fmt.Errorf("post request: %w", err)
		}
	}
	return nil
}

// post sends a plain notice; failures are logged.
func (g *Gateway) post(ctx context.Context, ch snowflake.ID, text string) {
	m := discord.NewMessageCreate().WithContent(render.Truncate(text, render.MessageLimit)).WithAllowedMentions(&discord.AllowedMentions{})
	if _, err := g.discord.CreateMessage(ch, m, rest.WithCtx(ctx)); err != nil {
		g.log.Warn("post notice", "channel", ch, "err", err)
	}
}
