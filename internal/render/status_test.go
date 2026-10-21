package render_test

import (
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/yyewolf/a2a-discord-gtw/internal/render"
)

func TestTitle(t *testing.T) {
	cases := map[string]string{
		"  how many\tnamespaces?\nsecond line": "how many namespaces?",
		"":                                     "conversation",
		strings.Repeat("é", 200):               strings.Repeat("é", 89) + "…",
	}
	for in, want := range cases {
		if got := render.Title(in); got != want {
			t.Errorf("Title(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStatusLine(t *testing.T) {
	got := render.StatusLine("windmill-maintainer", "Do your routine.", "scheduled", a2a.TaskStateInputRequired)
	want := "🙋 **windmill-maintainer** · needs input · scheduled\n> Do your routine."
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if !strings.HasPrefix(render.StatusLine("a", "t", "", a2a.TaskStateCompleted), "✅ **a** · done\n") {
		t.Fatal("completed line")
	}
}
