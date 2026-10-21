package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/yyewolf/a2a-discord-gtw/internal/cli"
)

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func TestVersion(t *testing.T) {
	out, err := run(t, "version")
	if err != nil || !strings.HasPrefix(out, "a2a-discord-gtw dev") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

func TestServeNeedsToken(t *testing.T) {
	t.Setenv("ADG_DISCORD_TOKEN", "")
	t.Setenv("ADG_DISCORD_TOKEN_FILE", "")
	_, err := run(t, "serve", "--data-dir", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "ADG_DISCORD_TOKEN") {
		t.Fatalf("err = %v", err)
	}
}

