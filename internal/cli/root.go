// Package cli defines the a2a-discord-gtw command tree.
package cli

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/yyewolf/a2a-discord-gtw/internal/config"
)

// env is what the subcommands share: the settings, and the logger
// PersistentPreRunE builds from them.
type env struct {
	cfg config.Config
	log *slog.Logger
}

// NewRootCmd builds the command tree.
func NewRootCmd() *cobra.Command {
	e := &env{cfg: config.FromEnv()}
	root := &cobra.Command{
		Use:           "a2a-discord-gtw",
		Short:         "Talk to kagent agents from Discord",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if err := e.cfg.Validate(); err != nil {
				return err
			}
			level, err := config.ParseLogLevel(e.cfg.LogLevel)
			if err != nil {
				return err
			}
			e.log = slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{Level: level}))
			return nil
		},
	}
	e.cfg.BindFlags(root.PersistentFlags())
	root.AddCommand(newVersionCmd(), newServeCmd(e), newHealthcheckCmd(e))
	return root
}

// Execute runs the CLI and returns the process exit code.
func Execute(ctx context.Context) int {
	root := NewRootCmd()
	if err := root.ExecuteContext(ctx); err != nil {
		_, _ = fmt.Fprintln(root.ErrOrStderr(), "error:", err) // nothing left to report a failed write to
		return 1
	}
	return 0
}
