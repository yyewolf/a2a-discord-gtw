package app_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yyewolf/a2a-discord-gtw/internal/app"
	"github.com/yyewolf/a2a-discord-gtw/internal/config"
)

const yamlConfig = `
discord:
  guild_id: "1027556538560282765"
  category_id: "1562536781361188945"
  allowed_user_ids: ["111111111111111111"]
kagent:
  url: http://127.0.0.1:1
agents:
  - name: claude-assistant
    ref: kagent/claude-assistant
`

func TestNew(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(good, []byte(yamlConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		path    string
		wantErr string
	}{
		{"valid", good, ""},
		{"missing file", filepath.Join(dir, "nope.yaml"), "read"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := config.Config{
				ConfigPath: c.path, DataDir: filepath.Join(dir, c.name), Listen: ":0", LogLevel: "info",
				// A syntactically valid token: base64("123") is the application ID.
				DiscordToken: "MTIz.abc.def",
			}
			a, err := app.New(context.Background(), cfg, slog.New(slog.DiscardHandler))
			if c.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				a.Close()
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("err = %v, want %q", err, c.wantErr)
			}
		})
	}
}
