// Package app wires the gateway together and runs it.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/disgoorg/snowflake/v2"

	"github.com/yyewolf/a2a-discord-gtw/internal/bot"
	"github.com/yyewolf/a2a-discord-gtw/internal/config"
	"github.com/yyewolf/a2a-discord-gtw/internal/discordbot"
	"github.com/yyewolf/a2a-discord-gtw/internal/health"
	"github.com/yyewolf/a2a-discord-gtw/internal/kagent"
	"github.com/yyewolf/a2a-discord-gtw/internal/store"
)

const shutdownTimeout = 10 * time.Second

// App is a configured, not yet running gateway.
type App struct {
	cfg     config.Config
	file    *config.File
	log     *slog.Logger
	store   *store.Store
	agents  []*kagent.Agent
	discord *discordbot.Bot
	gateway *bot.Gateway
}

// New loads the agent configuration, opens the state database and builds the
// clients. Nothing connects yet.
func New(ctx context.Context, cfg config.Config, log *slog.Logger) (_ *App, err error) {
	file, err := config.LoadFile(cfg.ConfigPath)
	if err != nil {
		return nil, err
	}
	a := &App{cfg: cfg, file: file, log: log}
	defer func() {
		if err != nil {
			a.Close()
		}
	}()
	if a.store, err = store.Open(ctx, cfg.DBPath()); err != nil {
		return nil, err
	}
	clients := map[string]bot.AgentClient{}
	for _, ag := range file.Agents {
		u, err := ag.URL(file.Kagent.URL)
		if err != nil {
			return nil, err
		}
		c, err := kagent.NewAgent(ctx, u, kagent.Options{UserID: file.Kagent.UserID})
		if err != nil {
			return nil, err
		}
		a.agents = append(a.agents, c)
		clients[ag.Name] = c
	}
	if a.discord, err = discordbot.New(cfg.DiscordToken, log); err != nil {
		return nil, err
	}
	a.gateway, err = bot.New(bot.Options{
		Config: file, Store: a.store, Discord: a.discord.Rest(), Agents: clients, Logger: log,
	})
	if err != nil {
		return nil, err
	}
	return a, nil
}

// Run provisions the agent channels, connects to Discord, serves the health
// endpoints and watches agents until ctx is done or something fails.
func (a *App) Run(ctx context.Context, ln net.Listener) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	srv := &http.Server{
		Handler:           health.Handler(a.checks()...),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		// Readiness checks are bounded at 3s; leave room to write.
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
		BaseContext:  func(net.Listener) context.Context { return ctx },
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			cancel(fmt.Errorf("health server: %w", err))
		}
	})
	defer func() {
		sctx, scancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer scancel()
		if err := srv.Shutdown(sctx); err != nil {
			a.log.Warn("health server shutdown", "err", err)
		}
		wg.Wait()
	}()

	if err := a.gateway.EnsureChannels(ctx); err != nil {
		return err
	}
	guild, err := snowflake.Parse(a.file.Discord.GuildID)
	if err != nil {
		return fmt.Errorf("app: guild id: %w", err)
	}
	if err := a.discord.Start(ctx, guild, a.gateway); err != nil {
		return err
	}
	defer func() {
		// Events in flight see ctx cancelled first, then the connection
		// closes and Close waits for them.
		cancel(nil)
		sctx, scancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer scancel()
		a.discord.Close(sctx)
	}()
	a.log.Info("connected to Discord", "guild", guild, "agents", len(a.file.Agents))

	wg.Go(func() { a.gateway.Watch(ctx) })

	<-ctx.Done()
	if err := context.Cause(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// checks are the readiness checks: Discord connected, state database usable,
// kagent reachable.
func (a *App) checks() []health.Check {
	return []health.Check{
		{Name: "discord", Fn: a.discord.Ready},
		{Name: "store", Fn: a.store.Ping},
		{Name: "kagent", Fn: func(ctx context.Context) error {
			_, err := a.agents[0].Card(ctx)
			return err
		}},
	}
}

// Close releases the database and clients.
func (a *App) Close() {
	for _, c := range a.agents {
		if err := c.Close(); err != nil {
			a.log.Warn("close agent client", "err", err)
		}
	}
	if a.store != nil {
		if err := a.store.Close(); err != nil {
			a.log.Warn("close store", "err", err)
		}
	}
}
