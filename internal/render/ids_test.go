package render_test

import (
	"strings"
	"testing"

	"github.com/yyewolf/a2a-discord-gtw/internal/render"
)

func TestIDRoundTrip(t *testing.T) {
	id := render.ID{Kind: render.KindRejectSubmit, Task: "01a15fd3-28b5-7a83-8cb1-60f4d69989fe", Key: render.ShortKey("156cdfea-eb43-4470-bd78-0d5d369497bd"), Index: 3, Arg: 24}
	s := id.String()
	if len(s) > 100 {
		t.Fatalf("custom id is %d characters", len(s))
	}
	got, err := render.ParseID(s)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != id.Kind || got.Task != id.Task || got.Index != 3 || got.Arg != 24 || !got.Matches("156cdfea-eb43-4470-bd78-0d5d369497bd") {
		t.Fatalf("got %+v", got)
	}
	if got.Matches("other-request") {
		t.Fatal("matched a different request")
	}
}

func TestShortKey(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		same bool
	}{
		{"same key", "toolu_01ABCDEFGH", "toolu_01ABCDEFGH", true},
		// Anthropic tool IDs share a long prefix; a prefix-based key let a
		// stale Approve button match a newer request.
		{"shared prefix", "toolu_01ABCDEFGH", "toolu_01ABCDXYZW", false},
		{"separators", "a:b", "ab", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ka, kb := render.ShortKey(c.a), render.ShortKey(c.b)
			if (ka == kb) != c.same {
				t.Fatalf("ShortKey(%q) = %q, ShortKey(%q) = %q", c.a, ka, c.b, kb)
			}
			if strings.Contains(ka, ":") || len(ka) != 12 {
				t.Fatalf("key %q", ka)
			}
		})
	}
}

func TestParseIDRejects(t *testing.T) {
	for _, s := range []string{"danger", "adg:c:t:k:1", "adg:c::k:1:2", "adg:c:t:k:x:2", "xyz:c:t:k:1:2", "adg:c:t:k:-1:0", "adg:c:t:k:0:-3", "adg:c:t:k:99999999999:0"} {
		if _, err := render.ParseID(s); err == nil {
			t.Errorf("ParseID(%q) accepted", s)
		}
	}
}

// A parsed ID re-encoded (as when a button opens a modal) still matches.
func TestIDReencodeKeepsKey(t *testing.T) {
	id := render.ID{Kind: render.KindOther, Task: "t", Key: render.ShortKey("req-1")}
	parsed, err := render.ParseID(id.String())
	if err != nil {
		t.Fatal(err)
	}
	parsed.Kind = render.KindOtherSubmit
	again, err := render.ParseID(parsed.String())
	if err != nil || !again.Matches("req-1") {
		t.Fatalf("re-encoded %+v, %v", again, err)
	}
}
