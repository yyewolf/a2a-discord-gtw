package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
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

func TestHealthcheck(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{"healthy", http.StatusOK, false},
		{"unhealthy", http.StatusServiceUnavailable, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/healthz" {
					http.NotFound(w, r)
					return
				}
				w.WriteHeader(c.status)
			}))
			defer srv.Close()
			_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
			_, err := run(t, "healthcheck", "--listen", fmt.Sprintf(":%s", port))
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v", err)
			}
		})
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

func TestRejectsBadLogLevel(t *testing.T) {
	if _, err := run(t, "serve", "--log-level", "loud"); err == nil || !strings.Contains(err.Error(), "log level") {
		t.Fatalf("err = %v", err)
	}
}

func TestHealthcheckIgnoresOtherSettings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if _, err := run(t, "healthcheck", "--listen", ":"+port, "--log-level", "loud", "--data-dir", ""); err != nil {
		t.Fatalf("err = %v", err)
	}
}
