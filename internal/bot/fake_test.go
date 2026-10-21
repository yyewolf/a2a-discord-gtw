package bot_test

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"path/filepath"
	"slices"
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

func (f *fakeDiscord) GetChannel(id snowflake.ID, _ ...rest.RequestOpt) (discord.Channel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ch, ok := f.channels[id]; ok {
		return ch, nil
	}
	return nil, unknownChannel()
}

func (f *fakeDiscord) GetGuildChannels(snowflake.ID, ...rest.RequestOpt) ([]discord.GuildChannel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []discord.GuildChannel
	for _, ch := range f.channels {
		out = append(out, ch)
	}
	return out, nil
}

func (f *fakeDiscord) CreateGuildChannel(_ snowflake.ID, c discord.GuildChannelCreate, _ ...rest.RequestOpt) (discord.GuildChannel, error) {
	tc, ok := c.(discord.GuildTextChannelCreate)
	if !ok {
		return nil, fmt.Errorf("fake: unsupported channel create %T", c)
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

func (f *fakeDiscord) CreateMessage(ch snowflake.ID, m discord.MessageCreate, _ ...rest.RequestOpt) (*discord.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.channels[ch]; !ok {
		if _, ok := f.threads[ch]; !ok {
			return nil, unknownChannel()
		}
	}
	msg := &fakeMessage{ID: f.id(), ChannelID: ch, Content: m.Content, Components: m.Components, Mentions: m.AllowedMentions}
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

func (f *fakeDiscord) UpdateMessage(ch, id snowflake.ID, u discord.MessageUpdate, _ ...rest.RequestOpt) (*discord.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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

func (f *fakeDiscord) CreateThreadFromMessage(ch, msgID snowflake.ID, t discord.ThreadCreateFromMessage, _ ...rest.RequestOpt) (*discord.GuildThread, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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

func (f *fakeDiscord) SendTyping(snowflake.ID, ...rest.RequestOpt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.typing++
	return nil
}

func (f *fakeDiscord) AddReaction(_, id snowflake.ID, emoji string, _ ...rest.RequestOpt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	msg, ok := f.messages[id]
	if !ok {
		return fmt.Errorf("fake: unknown message %s", id)
	}
	if !slices.Contains(msg.Reactions, emoji) {
		msg.Reactions = append(msg.Reactions, emoji)
	}
	return nil
}

func (f *fakeDiscord) RemoveOwnReaction(_, id snowflake.ID, emoji string, _ ...rest.RequestOpt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	msg, ok := f.messages[id]
	if !ok {
		return fmt.Errorf("fake: unknown message %s", id)
	}
	msg.Reactions = slices.DeleteFunc(msg.Reactions, func(e string) bool { return e == emoji })
	return nil
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
}

func newFakeAgent(description string) *fakeAgent {
	return &fakeAgent{description: description, tasks: map[a2a.TaskID]*a2a.Task{}}
}

func (a *fakeAgent) Card(context.Context) (*a2a.AgentCard, error) {
	if a.cardErr != nil {
		return nil, a.cardErr
	}
	return &a2a.AgentCard{Description: a.description}, nil
}

func (a *fakeAgent) Stream(_ context.Context, m *a2a.Message) iter.Seq2[a2a.Event, error] {
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
	return func(yield func(a2a.Event, error) bool) {
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
	slices.SortFunc(out, func(x, y *a2a.Task) int { return x.Status.Timestamp.Compare(*y.Status.Timestamp) })
	return out, nil
}

func (a *fakeAgent) PendingTask(_ context.Context, contextID string) (*a2a.Task, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, t := range a.tasks {
		if t.ContextID == contextID && t.Status.State == a2a.TaskStateInputRequired {
			cp := *t
			return &cp, nil
		}
	}
	return nil, kagent.ErrNoRequest
}

func (a *fakeAgent) lastSent() *a2a.Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.sent) == 0 {
		return nil
	}
	return a.sent[len(a.sent)-1]
}

// harness is a gateway wired to fakes.
type harness struct {
	t       *testing.T
	gw      *bot.Gateway
	discord *fakeDiscord
	store   *store.Store
	agents  map[string]*fakeAgent
	cfg     *config.File
}

func newHarness(t *testing.T, mutate ...func(*config.File)) *harness {
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
	h := &harness{t: t, discord: newFakeDiscord(), store: st, agents: map[string]*fakeAgent{}, cfg: cfg}
	clients := map[string]bot.AgentClient{}
	for _, a := range cfg.Agents {
		fa := newFakeAgent("The " + a.Name + " agent.")
		h.agents[a.Name] = fa
		clients[a.Name] = fa
	}
	h.gw, err = bot.New(bot.Options{Config: cfg, Store: st, Discord: h.discord, Agents: clients})
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
