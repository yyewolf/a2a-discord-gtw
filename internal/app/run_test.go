package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/yyewolf/a2a-discord-gtw/internal/config"
	"github.com/yyewolf/a2a-discord-gtw/internal/discordbot"
	"github.com/yyewolf/a2a-discord-gtw/internal/kagent/kagenttest"
)

// fakeConn stands in for the DisGo connection and records its lifecycle.
type fakeConn struct {
	mu     sync.Mutex
	events []string
	ready  error
	ctx    context.Context
}

func (f *fakeConn) note(e string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
}

func (f *fakeConn) Start(ctx context.Context, guild snowflake.ID, _ discordbot.Handler) error {
	f.note("start " + guild.String())
	f.mu.Lock()
	f.ctx = ctx
	f.mu.Unlock()
	return nil
}

func (f *fakeConn) Ready(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ready
}

func (f *fakeConn) Close(context.Context) {
	f.mu.Lock()
	cancelled := f.ctx.Err() != nil
	f.mu.Unlock()
	if cancelled {
		f.note("close after handlers' context ended")
	} else {
		f.note("close while handlers still run")
	}
}

// fakeRest is the REST side: an empty guild where channels can be created.
type fakeRest struct {
	rest.Rest // unused methods panic, which a test should notice
	mu        sync.Mutex
	next      snowflake.ID
}

func (f *fakeRest) GetChannel(snowflake.ID, ...rest.RequestOpt) (discord.Channel, error) {
	return nil, &rest.Error{Code: rest.JSONErrorCodeUnknownChannel}
}

func (f *fakeRest) GetGuildChannels(snowflake.ID, ...rest.RequestOpt) ([]discord.GuildChannel, error) {
	return nil, nil
}

func (f *fakeRest) CreateGuildChannel(guild snowflake.ID, c discord.GuildChannelCreate, _ ...rest.RequestOpt) (discord.GuildChannel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	tc := c.(discord.GuildTextChannelCreate)
	b, _ := json.Marshal(map[string]any{"id": f.next.String(), "type": discord.ChannelTypeGuildText, "guild_id": guild.String(), "name": tc.Name, "parent_id": tc.ParentID.String()})
	var ch discord.GuildTextChannel
	return ch, json.Unmarshal(b, &ch)
}

func TestRunLifecycle(t *testing.T) {
	agent := kagenttest.New()
	defer agent.Close()
	file := &config.File{
		Discord: config.Discord{GuildID: "1027556538560282765", CategoryID: "1562536781361188945", AllowedUserIDs: []string{"1"}, NotifyUserIDs: []string{"1"}},
		Kagent:  config.Kagent{URL: agent.URL},
		Watch:   config.Watch{Interval: time.Minute},
		Agents:  []config.Agent{{Name: "claude-assistant", Ref: "kagent/claude-assistant"}},
	}
	cfg := config.Config{DataDir: t.TempDir(), Listen: "127.0.0.1:0", LogLevel: "info"}
	cfg.ConfigPath = filepath.Join(cfg.DataDir, "unused.yaml")
	conn := &fakeConn{ready: errors.New("not connected")}
	a, err := build(context.Background(), cfg, file, slog.New(slog.DiscardHandler), conn, &fakeRest{next: 1600000000000000000})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx, ln) }()

	base := "http://" + ln.Addr().String()
	status := func(path string) int {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, base+path, nil)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0
		}
		defer func() { _ = res.Body.Close() }()
		return res.StatusCode
	}
	waitFor := func(path string, want int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for status(path) != want {
			if time.Now().After(deadline) {
				t.Fatalf("%s never answered %d", path, want)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	waitFor("/healthz", http.StatusOK)
	waitFor("/readyz", http.StatusServiceUnavailable) // Discord not connected yet
	conn.mu.Lock()
	conn.ready = nil
	conn.mu.Unlock()
	waitFor("/readyz", http.StatusOK)

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	conn.mu.Lock()
	defer conn.mu.Unlock()
	// Handlers see their context end before the connection closes.
	want := []string{"start 1027556538560282765", "close after handlers' context ended"}
	if !slices.Equal(conn.events, want) {
		t.Fatalf("lifecycle = %v, want %v", conn.events, want)
	}
	if status("/healthz") != 0 {
		t.Fatal("health server still serving after Run returned")
	}
}
