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

