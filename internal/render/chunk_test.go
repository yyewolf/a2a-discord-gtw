package render_test

import (
	"strings"
	"testing"

	"github.com/yyewolf/a2a-discord-gtw/internal/render"
)

func TestChunkShort(t *testing.T) {
	if got := render.Chunk("  hello  ", 2000); len(got) != 1 || got[0] != "hello" {
		t.Fatalf("got %q", got)
	}
	if got := render.Chunk("   ", 2000); got != nil {
		t.Fatalf("got %q", got)
	}
}

func TestChunkPrefersParagraphs(t *testing.T) {
	text := strings.Repeat("a", 30) + "\n\n" + strings.Repeat("b", 30)
	got := render.Chunk(text, 50)
	if len(got) != 2 || got[0] != strings.Repeat("a", 30) || got[1] != strings.Repeat("b", 30) {
		t.Fatalf("got %q", got)
	}
}

func TestChunkRespectsLimit(t *testing.T) {
	text := strings.Repeat("word ", 1000)
	for _, c := range render.Chunk(text, 2000) {
		if n := len([]rune(c)); n > 2000 {
			t.Fatalf("chunk of %d runes", n)
		}
	}
	// No boundary at all: hard cut.
	got := render.Chunk(strings.Repeat("x", 120), 50)
	if len(got) != 3 {
		t.Fatalf("got %d chunks", len(got))
	}
}

func TestChunkReopensCodeFences(t *testing.T) {
	code := "```go\n" + strings.Repeat("fmt.Println(1)\n", 20) + "```"
	got := render.Chunk("Here:\n"+code+"\nDone.", 120)
	if len(got) < 2 {
		t.Fatalf("got %q", got)
	}
	for i, c := range got {
		if strings.Count(c, "```")%2 != 0 {
			t.Fatalf("chunk %d has an unbalanced fence: %q", i, c)
		}
		if len([]rune(c)) > 120 {
			t.Fatalf("chunk %d too long: %d", i, len([]rune(c)))
		}
	}
	if !strings.HasPrefix(got[1], "```go\n") {
		t.Fatalf("second chunk does not reopen the fence: %q", got[1])
	}
}
