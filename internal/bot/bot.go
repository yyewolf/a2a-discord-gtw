// Package bot is the gateway itself: it maps Discord channels, threads and
// components onto kagent agents, conversations and paused tasks.
package bot

import (
	"context"
	"fmt"
	"iter"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/disgoorg/snowflake/v2"

	"github.com/yyewolf/a2a-discord-gtw/internal/config"
	"github.com/yyewolf/a2a-discord-gtw/internal/store"
)

// AgentClient is what the gateway needs from a kagent agent. *kagent.Agent
// implements it.
type AgentClient interface {
	Card(ctx context.Context) (*a2a.AgentCard, error)
	Stream(ctx context.Context, m *a2a.Message) iter.Seq2[a2a.Event, error]
	Task(ctx context.Context, id a2a.TaskID) (*a2a.Task, error)
	TasksSince(ctx context.Context, since time.Time) ([]*a2a.Task, error)
	PendingTask(ctx context.Context, contextID string) (*a2a.Task, error)
}

// Agent is one configured agent and its client.
type Agent struct {
	config.Agent
	Client AgentClient
}

// Options builds a Gateway.
type Options struct {
	Config  *config.File
	Store   *store.Store
	Discord Discord
	// Agents are keyed by their Discord name; every configured agent must be
	// present.
	Agents map[string]AgentClient
	Logger *slog.Logger
}

// Gateway connects Discord to kagent.
type Gateway struct {
	cfg      *config.File
	store    *store.Store
	discord  Discord
	log      *slog.Logger
	guild    snowflake.ID
	category snowflake.ID
	allowed  map[snowflake.ID]bool
	notify   []snowflake.ID
	agents   map[string]*Agent
	order    []string

	mu        sync.RWMutex
	byChannel map[snowflake.ID]string // agent channel -> agent name

	// conv serialises turns on one conversation (keyed by thread ID).
	conv keyedMutex
	// streaming holds the tasks a relay is following live; the watcher
	// leaves them alone.
	streaming sync.Map

	// Work started by an event but outliving it (a relay after a button
	// press) runs under runCtx and is waited for by Run.
	runCtx context.Context
	work   sync.WaitGroup
}

// New builds a Gateway. Call EnsureChannels before handling events.
func New(o Options) (*Gateway, error) {
	g := &Gateway{
		cfg:       o.Config,
		store:     o.Store,
		discord:   o.Discord,
		log:       o.Logger,
		allowed:   map[snowflake.ID]bool{},
		agents:    map[string]*Agent{},
		byChannel: map[snowflake.ID]string{},
	}
	if g.log == nil {
		g.log = slog.New(slog.DiscardHandler)
	}
	var err error
	if g.guild, err = parseID(o.Config.Discord.GuildID); err != nil {
		return nil, err
	}
	if g.category, err = parseID(o.Config.Discord.CategoryID); err != nil {
		return nil, err
	}
	for _, s := range o.Config.Discord.AllowedUserIDs {
		id, err := parseID(s)
		if err != nil {
			return nil, err
		}
		g.allowed[id] = true
	}
	for _, s := range o.Config.Discord.NotifyUserIDs {
		id, err := parseID(s)
		if err != nil {
			return nil, err
		}
		g.notify = append(g.notify, id)
	}
	for _, a := range o.Config.Agents {
		c, ok := o.Agents[a.Name]
		if !ok {
			return nil, fmt.Errorf("bot: no client for agent %q", a.Name)
		}
		g.agents[a.Name] = &Agent{Agent: a, Client: c}
		g.order = append(g.order, a.Name)
	}
	return g, nil
}

// Allowed reports whether a user may talk to agents.
func (g *Gateway) Allowed(user snowflake.ID) bool { return g.allowed[user] }

// AgentNames lists the configured agents in configuration order.
func (g *Gateway) AgentNames() []string { return append([]string(nil), g.order...) }

// agentForChannel returns the agent whose channel this is.
func (g *Gateway) agentForChannel(ch snowflake.ID) (*Agent, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	name, ok := g.byChannel[ch]
	if !ok {
		return nil, false
	}
	return g.agents[name], true
}

func (g *Gateway) channelOf(agent string) (snowflake.ID, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	for ch, name := range g.byChannel {
		if name == agent {
			return ch, true
		}
	}
	return 0, false
}

func parseID(s string) (snowflake.ID, error) {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("bot: invalid Discord ID %q", s)
	}
	return snowflake.ID(n), nil
}

// keyedMutex is a set of mutexes created on demand.
type keyedMutex struct {
	mu    sync.Mutex
	locks map[snowflake.ID]*sync.Mutex
}

func (k *keyedMutex) lock(key snowflake.ID) func() {
	k.mu.Lock()
	if k.locks == nil {
		k.locks = map[snowflake.ID]*sync.Mutex{}
	}
	m, ok := k.locks[key]
	if !ok {
		m = &sync.Mutex{}
		k.locks[key] = m
	}
	k.mu.Unlock()
	m.Lock()
	return m.Unlock
}

// Run owns the gateway's background work: it blocks until ctx is done, then
// waits for work started with spawn to finish.
func (g *Gateway) Run(ctx context.Context) {
	g.mu.Lock()
	g.runCtx = ctx
	g.mu.Unlock()
	<-ctx.Done()
	g.work.Wait()
}

// spawn runs f in the background under Run's context, so it is cancelled on
// shutdown and Run waits for it. Before Run starts, f gets ctx.
func (g *Gateway) spawn(ctx context.Context, f func(ctx context.Context)) {
	g.mu.RLock()
	if g.runCtx != nil {
		ctx = g.runCtx
	}
	g.mu.RUnlock()
	g.work.Go(func() { f(ctx) })
}
