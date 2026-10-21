package render_test

import (
	"testing"

	"github.com/yyewolf/a2a-discord-gtw/internal/render"
)

func TestIDRoundTrip(t *testing.T) {
	id := render.ID{Kind: render.KindRejectSubmit, Task: "01a15fd3-28b5-7a83-8cb1-60f4d69989fe", Key: "156cdfea-eb43-4470-bd78-0d5d369497bd", Index: 3, Arg: 24}
	s := id.String()
	if len(s) > 100 {
		t.Fatalf("custom id is %d characters", len(s))
	}
	got, err := render.ParseID(s)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != id.Kind || got.Task != id.Task || got.Index != 3 || got.Arg != 24 || !got.Matches(id.Key) {
		t.Fatalf("got %+v", got)
	}
	if got.Matches("other-request") {
		t.Fatal("matched a different request")
	}
}

func TestShortKeyStripsSeparators(t *testing.T) {
	if got := render.ShortKey("a:b:c"); got != "abc" {
		t.Fatalf("got %q", got)
	}
}

func TestParseIDRejects(t *testing.T) {
	for _, s := range []string{"danger", "adg:c:t:k:1", "adg:c::k:1:2", "adg:c:t:k:x:2", "xyz:c:t:k:1:2"} {
		if _, err := render.ParseID(s); err == nil {
			t.Errorf("ParseID(%q) accepted", s)
		}
	}
}
