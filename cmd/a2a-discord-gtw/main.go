// Command a2a-discord-gtw lets you talk to kagent agents from Discord.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	_ "time/tzdata" // the scratch image has no zoneinfo

	_ "golang.org/x/crypto/x509roots/fallback" // the scratch image has no CA bundle

	"github.com/yyewolf/a2a-discord-gtw/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// Once the first signal starts the graceful shutdown, give the signals
	// back to the runtime: a second one kills the process.
	context.AfterFunc(ctx, stop)
	code := cli.Execute(ctx)
	stop()
	os.Exit(code)
}
