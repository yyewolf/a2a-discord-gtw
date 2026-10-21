package cli

import (
	"net"

	"github.com/spf13/cobra"

	"github.com/yyewolf/a2a-discord-gtw/internal/app"
	"github.com/yyewolf/a2a-discord-gtw/internal/buildinfo"
)

func newServeCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Connect to Discord and relay to kagent",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := e.cfg.LoadToken(); err != nil {
				return err
			}
			a, err := app.New(cmd.Context(), e.cfg, e.log)
			if err != nil {
				return err
			}
			defer a.Close()
			var lc net.ListenConfig
			ln, err := lc.Listen(cmd.Context(), "tcp", e.cfg.Listen)
			if err != nil {
				return err
			}
			e.log.Info("a2a-discord-gtw started", "health", ln.Addr().String(), "config", e.cfg.ConfigPath, "version", buildinfo.Version)
			return a.Run(cmd.Context(), ln)
		},
	}
}
