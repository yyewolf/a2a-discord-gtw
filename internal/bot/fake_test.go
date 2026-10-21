package bot_test

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"log/slog"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/yyewolf/a2a-discord-gtw/internal/bot"
	"github.com/yyewolf/a2a-discord-gtw/internal/config"
	"github.com/yyewolf/a2a-discord-gtw/internal/kagent"
	"github.com/yyewolf/a2a-discord-gtw/internal/store"
)

const (
	guildID    = snowflake.ID(1027556538560282765)
	categoryID = snowflake.ID(1562536781361188945)
	ownerID    = snowflake.ID(111111111111111111)
	strangerID = snowflake.ID(222222222222222222)
)

// fakeMessage is a message as the fake Discord holds it.
type fakeMessage struct {
	ID         snowflake.ID
	ChannelID  snowflake.ID
	Content    string
	Components []discord.LayoutComponent
	Mentions   *discord.AllowedMentions
	Reactions  []string
}

// fakeDiscord implements bot.Discord in memory.
type fakeDiscord struct {
	mu       sync.Mutex
	next     snowflake.ID
	channels map[snowflake.ID]discord.GuildChannel
	messages map[snowflake.ID]*fakeMessage
	order    []snowflake.ID
	threads  map[snowflake.ID]snowflake.ID // thread -> parent channel
	typing   int
	// failChannel, when set, can fail a channel creation.
	failChannel func(name string) error
	// failCreate, when set, can fail a message creation.
	failCreate func(m discord.MessageCreate) error
	// created logs the content of every message ever created, including
	// ones deleted since.
	created []string
	// noCtx counts calls made without request options: every call must
	// carry its context (rest.WithCtx), or it cannot be cancelled.
	noCtx int
}

func (f *fakeDiscord) check(opts []rest.RequestOpt) {
	if len(opts) == 0 {
		f.noCtx++
	}
}

func (f *fakeDiscord) checkLocked(opts []rest.RequestOpt) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.check(opts)
}

func newFakeDiscord() *fakeDiscord {
	return &fakeDiscord{
		next:     snowflake.ID(1600000000000000000),
		channels: map[snowflake.ID]discord.GuildChannel{},
		messages: map[snowflake.ID]*fakeMessage{},
		threads:  map[snowflake.ID]snowflake.ID{},
	}
}

func (f *fakeDiscord) id() snowflake.ID {
	f.next++
	return f.next
}

func missingPermissions() error {
	return &rest.Error{Code: rest.JSONErrorCodeLackPermissionsToPerformAction, Message: "Missing Permissions", Response: &http.Response{StatusCode: http.StatusForbidden}}
}

func unknownChannel() error {
	return &rest.Error{Code: rest.JSONErrorCodeUnknownChannel, Message: "Unknown Channel", Response: &http.Response{StatusCode: http.StatusNotFound}}
}

// textChannel builds a channel the way DisGo does, from Discord's JSON.
func textChannel(id snowflake.ID, name string, parent snowflake.ID, topic string) discord.GuildTextChannel {
	var ch discord.GuildTextChannel
	b, _ := json.Marshal(map[string]any{
		"id": id.String(), "type": discord.ChannelTypeGuildText, "guild_id": guildID.String(),
		"name": name, "parent_id": parent.String(), "topic": topic,
	})
	if err := json.Unmarshal(b, &ch); err != nil {
		panic(err)
	}
	return ch
}

func (f *fakeDiscord) addTextChannel(name string, parent snowflake.ID, topic string) snowflake.ID {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.id()
	f.channels[id] = textChannel(id, name, parent, topic)
	return id
}

func (f *fakeDiscord) GetChannel(id snowflake.ID, opts ...rest.RequestOpt) (discord.Channel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.check(opts)
	if ch, ok := f.channels[id]; ok {
		return ch, nil
	}
	return nil, unknownChannel()
}

func (f *fakeDiscord) GetGuildChannels(_ snowflake.ID, opts ...rest.RequestOpt) ([]discord.GuildChannel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.check(opts)
	var out []discord.GuildChannel
	for _, ch := range f.channels {
		out = append(out, ch)
	}
	return out, nil
}

func (f *fakeDiscord) CreateGuildChannel(_ snowflake.ID, c discord.GuildChannelCreate, opts ...rest.RequestOpt) (discord.GuildChannel, error) {
	f.checkLocked(opts)
	tc, ok := c.(discord.GuildTextChannelCreate)
	if !ok {
		return nil, fmt.Errorf("fake: unsupported channel create %T", c)
	}
	f.mu.Lock()
	fail := f.failChannel
	f.mu.Unlock()
	if fail != nil {
		if err := fail(tc.Name); err != nil {
			return nil, err
		}
	}
	id := f.addTextChannel(tc.Name, tc.ParentID, tc.Topic)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.channels[id], nil
}

func (f *fakeDiscord) deleteChannel(id snowflake.ID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.channels, id)
}

func (f *fakeDiscord) CreateMessage(ch snowflake.ID, m discord.MessageCreate, opts ...rest.RequestOpt) (*discord.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.check(opts)
	if _, ok := f.channels[ch]; !ok {
		if _, ok := f.threads[ch]; !ok {
			return nil, unknownChannel()
		}
	}
	if f.failCreate != nil {
		if err := f.failCreate(m); err != nil {
			return nil, err
		}
	}
	msg := &fakeMessage{ID: f.id(), ChannelID: ch, Content: m.Content, Components: m.Components, Mentions: m.AllowedMentions}
	f.created = append(f.created, m.Content)
	f.messages[msg.ID] = msg
	f.order = append(f.order, msg.ID)
	return &discord.Message{ID: msg.ID, ChannelID: ch, Content: m.Content}, nil
}

// userMessage records a message a user sent, as Discord would before the
// gateway sees it.
func (f *fakeDiscord) userMessage(ch snowflake.ID, content string) snowflake.ID {
	f.mu.Lock()
	defer f.mu.Unlock()
	msg := &fakeMessage{ID: f.id(), ChannelID: ch, Content: content}
	f.messages[msg.ID] = msg
	f.order = append(f.order, msg.ID)
	return msg.ID
}

func (f *fakeDiscord) UpdateMessage(ch, id snowflake.ID, u discord.MessageUpdate, opts ...rest.RequestOpt) (*discord.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.check(opts)
	msg, ok := f.messages[id]
	if !ok || msg.ChannelID != ch {
		return nil, fmt.Errorf("fake: unknown message %s", id)
	}
	if u.Content != nil {
		msg.Content = *u.Content
	}
	if u.Components != nil {
		msg.Components = *u.Components
	}
	return &discord.Message{ID: id, ChannelID: ch, Content: msg.Content}, nil
}

func (f *fakeDiscord) CreateThreadFromMessage(ch, msgID snowflake.ID, t discord.ThreadCreateFromMessage, opts ...rest.RequestOpt) (*discord.GuildThread, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.check(opts)
	if _, ok := f.messages[msgID]; !ok {
		return nil, fmt.Errorf("fake: unknown message %s", msgID)
	}
	// Discord gives a thread started from a message the message's ID.
	f.threads[msgID] = ch
	var th discord.GuildThread
	b, _ := json.Marshal(map[string]any{
		"id": msgID.String(), "type": discord.ChannelTypeGuildPublicThread, "guild_id": guildID.String(),
		"name": t.Name, "parent_id": ch.String(),
	})
	if err := json.Unmarshal(b, &th); err != nil {
		return nil, err
	}
	return &th, nil
}

func (f *fakeDiscord) SendTyping(_ snowflake.ID, opts ...rest.RequestOpt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.check(opts)
	f.typing++
	return nil
}

func (f *fakeDiscord) AddReaction(_, id snowflake.ID, emoji string, opts ...rest.RequestOpt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.check(opts)
	msg, ok := f.messages[id]
	if !ok {
		return fmt.Errorf("fake: unknown message %s", id)
	}
	if !slices.Contains(msg.Reactions, emoji) {
		msg.Reactions = append(msg.Reactions, emoji)
	}
	return nil
}

func (f *fakeDiscord) RemoveOwnReaction(_, id snowflake.ID, emoji string, opts ...rest.RequestOpt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.check(opts)
	msg, ok := f.messages[id]
	if !ok {
		return fmt.Errorf("fake: unknown message %s", id)
	}
	msg.Reactions = slices.DeleteFunc(msg.Reactions, func(e string) bool { return e == emoji })
	return nil
}

func (f *fakeDiscord) DeleteMessage(ch, id snowflake.ID, opts ...rest.RequestOpt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.check(opts)
	msg, ok := f.messages[id]
	if !ok || msg.ChannelID != ch {
		return fmt.Errorf("fake: unknown message %s", id)
	}
	delete(f.messages, id)
	f.order = slices.DeleteFunc(f.order, func(m snowflake.ID) bool { return m == id })
	return nil
}

func (f *fakeDiscord) DeleteChannel(id snowflake.ID, opts ...rest.RequestOpt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.check(opts)
	if _, ok := f.threads[id]; ok {
		delete(f.threads, id)
		return nil
	}
	if _, ok := f.channels[id]; ok {
		delete(f.channels, id)
		return nil
	}
	return unknownChannel()
}

// in returns a copy of the messages of a channel or thread, oldest first.
func (f *fakeDiscord) in(ch snowflake.ID) []fakeMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeMessage
	for _, id := range f.order {
		if m := f.messages[id]; m.ChannelID == ch {
			out = append(out, *m)
		}
	}
	return out
}

func (f *fakeDiscord) message(id snowflake.ID) fakeMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return *f.messages[id]
}

func (f *fakeDiscord) channelByName(name string) (discord.GuildTextChannel, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ch := range f.channels {
		if tc, ok := ch.(discord.GuildTextChannel); ok && tc.Name() == name {
			return tc, true
		}
	}
	return discord.GuildTextChannel{}, false
}

// fakeAgent implements bot.AgentClient with scripted behaviour.
type fakeAgent struct {
	mu          sync.Mutex
	description string
	cardErr     error
	tasks       map[a2a.TaskID]*a2a.Task
	sent        []*a2a.Message
	onSend      func(m *a2a.Message) ([]a2a.Event, error)
	// pendingErr, when set, is what PendingTask fails with.
	pendingErr error
	// taskErr makes the next Task call for an ID fail once.
	taskErr map[a2a.TaskID]error
	// onStream, when set, replaces onSend with a hand-written stream, for
	// tests that need timing.
	onStream func(ctx context.Context, m *a2a.Message) iter.Seq2[a2a.Event, error]
}

func newFakeAgent(description string) *fakeAgent {
	return &fakeAgent{description: description, tasks: map[a2a.TaskID]*a2a.Task{}, taskErr: map[a2a.TaskID]error{}}
}

func (a *fakeAgent) Card(context.Context) (*a2a.AgentCard, error) {
	if a.cardErr != nil {
		return nil, a.cardErr
	}
	return &a2a.AgentCard{Description: a.description}, nil
}

// Stream is lazy like the real client: the message is sent when the stream
// is first read.
func (a *fakeAgent) Stream(ctx context.Context, m *a2a.Message) iter.Seq2[a2a.Event, error] {
	a.mu.Lock()
	custom := a.onStream
	a.mu.Unlock()
	if custom != nil {
		return custom(ctx, m)
	}
	return func(yield func(a2a.Event, error) bool) {
		a.mu.Lock()
		a.sent = append(a.sent, m)
		f := a.onSend
		a.mu.Unlock()
		events, err := f(m)
		for _, ev := range events {
			if t, ok := ev.(*a2a.Task); ok {
				a.put(t)
			}
		}
		for _, ev := range events {
			if !yield(ev, nil) {
				return
			}
		}
		if err != nil {
			yield(nil, err)
		}
	}
}

func (a *fakeAgent) put(t *a2a.Task) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if t.Status.Timestamp == nil {
		now := time.Now()
		t.Status.Timestamp = &now
	}
	cp := *t
	a.tasks[t.ID] = &cp
}

func (a *fakeAgent) Task(_ context.Context, id a2a.TaskID) (*a2a.Task, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err, ok := a.taskErr[id]; ok {
		delete(a.taskErr, id)
		return nil, err
	}
	t, ok := a.tasks[id]
	if !ok {
		return nil, fmt.Errorf("fake: no task %s", id)
	}
	cp := *t
	return &cp, nil
}

func (a *fakeAgent) TasksSince(_ context.Context, since time.Time) ([]*a2a.Task, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []*a2a.Task
	for _, t := range a.tasks {
		if t.Status.Timestamp.After(since) {
			cp := *t
			out = append(out, &cp)
		}
	}
	// Newest first, as a2a-go's in-memory store lists them: the gateway must
	// not depend on the server's order.
	slices.SortFunc(out, func(x, y *a2a.Task) int { return y.Status.Timestamp.Compare(*x.Status.Timestamp) })
	return out, nil
}

func (a *fakeAgent) PendingTask(_ context.Context, contextID string) (*a2a.Task, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pendingErr != nil {
		return nil, a.pendingErr
	}
	for _, t := range a.tasks {
		if t.ContextID == contextID && t.Status.State == a2a.TaskStateInputRequired {
			cp := *t
			return &cp, nil
		}
	}
	return nil, kagent.ErrNoRequest
}

// harness is a gateway wired to fakes.
type harness struct {
	t       *testing.T
	gw      *bot.Gateway
	discord *fakeDiscord
	store   *store.Store
	agents  map[string]*fakeAgent
	cfg     *config.File
	options []func(*bot.Options)
}

func newHarness(t *testing.T, mutate ...func(*config.File)) *harness {
	return newHarnessWith(t, nil, mutate...)
}

// newHarnessWith also adjusts the gateway's options.
func newHarnessWith(t *testing.T, options []func(*bot.Options), mutate ...func(*config.File)) *harness {
	t.Helper()
	cfg := &config.File{
		Discord: config.Discord{
			GuildID: guildID.String(), CategoryID: categoryID.String(),
			AllowedUserIDs: []string{ownerID.String()}, NotifyUserIDs: []string{ownerID.String()},
		},
		Kagent: config.Kagent{URL: "http://kagent.invalid"},
		Watch:  config.Watch{Interval: time.Minute},
		Agents: []config.Agent{
			{Name: "claude-assistant", Ref: "kagent/claude-assistant"},
			{Name: "windmill-maintainer", Ref: "kagent/windmill-maintainer", Watch: true},
		},
	}
	for _, m := range mutate {
		m(cfg)
	}
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	h := &harness{t: t, discord: newFakeDiscord(), store: st, agents: map[string]*fakeAgent{}, cfg: cfg, options: options}
	clients := map[string]bot.AgentClient{}
	for _, a := range cfg.Agents {
		fa := newFakeAgent("The " + a.Name + " agent.")
		h.agents[a.Name] = fa
		clients[a.Name] = fa
	}
	t.Cleanup(func() {
		h.discord.mu.Lock()
		defer h.discord.mu.Unlock()
		if h.discord.noCtx > 0 {
			t.Errorf("%d Discord calls without a context", h.discord.noCtx)
		}
	})
	opts := bot.Options{Config: cfg, Store: st, Discord: h.discord, Agents: clients, Logger: slog.New(slog.NewTextHandler(testWriter{t}, nil))}
	for _, o := range h.options {
		o(&opts)
	}
	h.gw, err = bot.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *harness) ensure() {
	h.t.Helper()
	if err := h.gw.EnsureChannels(context.Background()); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) channel(agent string) snowflake.ID {
	h.t.Helper()
	ch, ok := h.discord.channelByName(agent)
	if !ok {
		h.t.Fatalf("no channel for %s", agent)
	}
	return ch.ID()
}

// testWriter sends the gateway's logs to the test log.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Helper()
	w.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}

func (f *fakeDiscord) everCreated() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.created...)
}
