package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/yyewolf/a2a-discord-gtw/internal/config"
)

const validYAML = `
discord:
  guild_id: "1027556538560282765"
  category_id: "1562536781361188945"
  allowed_user_ids: ["111111111111111111"]
kagent:
  url: http://kagent-controller.kagent.svc.cluster.local:8083/
agents:
  - name: claude-assistant
    ref: kagent/claude-assistant
  - name: windmill-maintainer
    ref: kagent/windmill-maintainer
    watch: true
`

func TestParseFileDefaults(t *testing.T) {
	f, err := config.ParseFile([]byte(validYAML))
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Discord.NotifyUserIDs; len(got) != 1 || got[0] != "111111111111111111" {
		t.Fatalf("notify users default = %v", got)
	}
	if f.Watch.Interval != 20*time.Second {
		t.Fatalf("watch interval = %s", f.Watch.Interval)
	}
	a, ok := f.Agent("windmill-maintainer")
	if !ok || !a.Watch {
		t.Fatalf("agent = %+v, %v", a, ok)
	}
	if got := a.URL(f.Kagent.URL); got != "http://kagent-controller.kagent.svc.cluster.local:8083/agents/kagent/windmill-maintainer" {
		t.Fatalf("url = %q", got)
	}
}

func TestParseFileRejects(t *testing.T) {
	cases := map[string]struct{ from, to, want string }{
		"unknown key":    {"kagent:", "kagnet:", "field kagnet not found"},
		"bad category":   {`"1562536781361188945"`, `"general"`, "category_id"},
		"no allowlist":   {`allowed_user_ids: ["111111111111111111"]`, `allowed_user_ids: []`, "allowed_user_ids"},
		"bad url":        {"url: http://kagent", "url: kagent", "kagent.url"},
		"bad name":       {"name: claude-assistant", "name: Claude Assistant", "channel name"},
		"bad ref":        {"ref: kagent/claude-assistant", "ref: claude-assistant", "namespace/name"},
		"duplicate name": {"name: windmill-maintainer", "name: claude-assistant", "used twice"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			src := strings.Replace(validYAML, c.from, c.to, 1)
			_, err := config.ParseFile([]byte(src))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to mention %q", err, c.want)
			}
		})
	}
}

func TestParseFileWatchInterval(t *testing.T) {
	_, err := config.ParseFile([]byte(validYAML + "watch:\n  interval: 1s\n"))
	if err == nil || !strings.Contains(err.Error(), "watch.interval") {
		t.Fatalf("err = %v", err)
	}
	f, err := config.ParseFile([]byte(validYAML + "watch:\n  interval: 45s\n"))
	if err != nil || f.Watch.Interval != 45*time.Second {
		t.Fatalf("f = %+v, err = %v", f, err)
	}
}
