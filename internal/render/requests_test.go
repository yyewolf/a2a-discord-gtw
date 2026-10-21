package render_test

import (
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/yyewolf/a2a-discord-gtw/internal/kagent"
	"github.com/yyewolf/a2a-discord-gtw/internal/render"
)

const owner = snowflake.ID(111111111111111111)

func ids(t *testing.T, m discord.MessageCreate) []render.ID {
	t.Helper()
	var out []render.ID
	for _, row := range m.Components {
		ar, ok := row.(discord.ActionRowComponent)
		if !ok {
			t.Fatalf("component %T", row)
		}
		for _, c := range ar.Components {
			var custom string
			switch c := c.(type) {
			case discord.ButtonComponent:
				custom = c.CustomID
			case discord.StringSelectMenuComponent:
				custom = c.CustomID
			}
			id, err := render.ParseID(custom)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, id)
		}
	}
	return out
}

func TestSingleChoiceQuestionButtons(t *testing.T) {
	req := &kagent.Request{ID: "req-123456789", Questions: []kagent.Question{{Question: "Which color?", Choices: []string{"Red", "Blue"}}}}
	msgs := render.QuestionMessages("claude-assistant", "task-1", req, []snowflake.ID{owner})
	if len(msgs) != 1 {
		t.Fatalf("%d messages", len(msgs))
	}
	m := msgs[0]
	if !strings.HasPrefix(m.Content, "<@111111111111111111> **claude-assistant** asks:\n> Which color?") {
		t.Fatalf("content = %q", m.Content)
	}
	if m.AllowedMentions == nil || len(m.AllowedMentions.Users) != 1 || m.AllowedMentions.Users[0] != owner {
		t.Fatalf("allowed mentions = %+v", m.AllowedMentions)
	}
	got := ids(t, m)
	if len(got) != 3 || got[0].Kind != render.KindChoice || got[1].Arg != 1 || got[2].Kind != render.KindOther {
		t.Fatalf("ids = %+v", got)
	}
	for _, id := range got {
		if id.Task != "task-1" || !id.Matches("req-123456789") {
			t.Fatalf("id = %+v", id)
		}
	}
}

func TestMultipleChoiceQuestionSelect(t *testing.T) {
	req := &kagent.Request{ID: "r", Questions: []kagent.Question{
		{Question: "Which?", Choices: []string{"a", "b", "c"}, Multiple: true},
		{Question: "Why?"},
	}}
	msgs := render.QuestionMessages("a", "t", req, nil)
	if len(msgs) != 2 || !strings.Contains(msgs[0].Content, "asks (1/2)") {
		t.Fatalf("msgs = %+v", msgs)
	}
	sel := msgs[0].Components[0].(discord.ActionRowComponent).Components[0].(discord.StringSelectMenuComponent)
	if sel.MinValues == nil || *sel.MinValues != 1 || sel.MaxValues != 3 || len(sel.Options) != 3 {
		t.Fatalf("select = %+v", sel)
	}
	// A question without choices only offers free text.
	free := ids(t, msgs[1])
	if len(free) != 1 || free[0].Kind != render.KindOther || free[0].Index != 1 {
		t.Fatalf("free = %+v", free)
	}
}

func TestManyChoicesFallBackToSelect(t *testing.T) {
	choices := make([]string, 30)
	for i := range choices {
		choices[i] = strings.Repeat("x", i+1)
	}
	req := &kagent.Request{ID: "r", Questions: []kagent.Question{{Question: "?", Choices: choices}}}
	m := render.QuestionMessages("a", "t", req, nil)[0]
	sel := m.Components[0].(discord.ActionRowComponent).Components[0].(discord.StringSelectMenuComponent)
	if len(sel.Options) != 25 {
		t.Fatalf("%d options", len(sel.Options))
	}
	if !strings.Contains(m.Content, "Only the first 25 of 30 choices") {
		t.Fatalf("content = %q", m.Content)
	}
}

func TestApprovalMessage(t *testing.T) {
	req := &kagent.Request{Hint: "Allow these calls?", Tools: []kagent.ToolCall{
		{ID: "tool-1", Name: "delete_file", Args: map[string]any{"path": "/tmp/x"}},
		{ID: "tool-2", Name: "restart_pod"},
	}}
	m := render.ApprovalMessage("windmill-maintainer", "t", req, []snowflake.ID{owner})
	for _, want := range []string{"**windmill-maintainer** wants to run", "> Allow these calls?", "**1.** `delete_file`", `"path": "/tmp/x"`, "**2.** `restart_pod`"} {
		if !strings.Contains(m.Content, want) {
			t.Errorf("content lacks %q: %s", want, m.Content)
		}
	}
	got := ids(t, m)
	kinds := ""
	for _, id := range got {
		kinds += string(id.Kind) + ","
	}
	if kinds != "a,r,a,r,aa,ra," {
		t.Fatalf("kinds = %s", kinds)
	}
	if !got[2].Matches("tool-1") || got[2].Index != 1 {
		t.Fatalf("second approve = %+v", got[2])
	}
}

func TestAnswered(t *testing.T) {
	u := render.Answered("question", "**Blue**", owner, "")
	if u.Content == nil || *u.Content != "question\n-# ↳ **Blue** by <@111111111111111111>" {
		t.Fatalf("content = %v", u.Content)
	}
	if u.Components == nil || len(*u.Components) != 0 {
		t.Fatalf("components = %v", u.Components)
	}
	u = render.Answered("question", "**a**", owner, "waiting for the other answers")
	if !strings.HasSuffix(*u.Content, "by <@111111111111111111> — waiting for the other answers") {
		t.Fatalf("content = %q", *u.Content)
	}
}

func TestRecoverFromRenderedRequests(t *testing.T) {
	q := render.QuestionMessages("a", "t", &kagent.Request{ID: "r", Questions: []kagent.Question{{Question: "Force-push\nprimary?"}}}, []snowflake.ID{owner})[0]
	if got := render.QuestionIn(q.Content); got != "Force-push primary?" {
		t.Fatalf("question = %q", got)
	}
	if got := render.QuestionIn("no quote here"); got != "Your answer" {
		t.Fatalf("fallback = %q", got)
	}
	a := render.ApprovalMessage("a", "t", &kagent.Request{Tools: []kagent.ToolCall{{ID: "1", Name: "delete_file"}, {ID: "2", Name: "restart_pod"}}}, nil)
	for i, want := range []string{"delete_file", "restart_pod", "this call"} {
		if got := render.ToolIn(a.Content, i); got != want {
			t.Errorf("tool %d = %q, want %q", i, got, want)
		}
	}
}
