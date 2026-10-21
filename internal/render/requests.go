package render

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/yyewolf/a2a-discord-gtw/internal/kagent"
)

// TextInputID is the custom ID of the free-text field in answer modals.
const TextInputID = "answer"

const (
	labelLimit      = 80 // button label
	optionLimit     = 100
	modalTitleLimit = 45
	maxButtonRows   = 4 // the fifth row is reserved for "Other…" or "… all"
	buttonsPerRow   = 5
	maxSelectValues = 25
	argsLimit       = 400
)

// Ping is the mention prefix for users to notify, and the allowed-mentions
// that make Discord actually notify them (and nobody else).
func Ping(users []snowflake.ID) (string, *discord.AllowedMentions) {
	var b strings.Builder
	for _, u := range users {
		b.WriteString("<@")
		b.WriteString(u.String())
		b.WriteString("> ")
	}
	return b.String(), &discord.AllowedMentions{Users: users}
}

// QuestionMessages renders an ask-user request, one message per question.
func QuestionMessages(agent, taskID string, req *kagent.Request, notify []snowflake.ID) []discord.MessageCreate {
	ping, allowed := Ping(notify)
	out := make([]discord.MessageCreate, 0, len(req.Questions))
	for i, q := range req.Questions {
		var b strings.Builder
		b.WriteString(ping)
		fmt.Fprintf(&b, "**%s** asks", agent)
		if len(req.Questions) > 1 {
			fmt.Fprintf(&b, " (%d/%d)", i+1, len(req.Questions))
		}
		b.WriteString(":\n")
		b.WriteString(quote(q.Question))
		if q.Multiple {
			b.WriteString("\n-# Pick one or more.")
		}
		m := discord.NewMessageCreate().
			WithContent(Truncate(b.String(), MessageLimit)).
			WithAllowedMentions(allowed)
		for _, row := range questionRows(taskID, req.Key(), i, q) {
			m = m.AddActionRow(row...)
		}
		out = append(out, m)
	}
	return out
}

func questionRows(taskID, key string, qi int, q kagent.Question) [][]discord.InteractiveComponent {
	id := func(kind Kind, arg int) string {
		return ID{Kind: kind, Task: taskID, Key: ShortKey(key), Index: qi, Arg: arg}.String()
	}
	var rows [][]discord.InteractiveComponent
	if len(q.Choices) > 0 && (q.Multiple || len(q.Choices) > maxButtonRows*buttonsPerRow) {
		opts := make([]discord.StringSelectMenuOption, 0, len(q.Choices))
		for ci, c := range q.Choices {
			if ci == maxSelectValues {
				break
			}
			opts = append(opts, discord.NewStringSelectMenuOption(Truncate(c, optionLimit), strconv.Itoa(ci)))
		}
		menu := discord.NewStringSelectMenu(id(KindSelect, 0), "Choose…", opts...)
		if q.Multiple {
			menu = menu.WithMinValues(1).WithMaxValues(len(opts))
		}
		rows = append(rows, []discord.InteractiveComponent{menu})
	} else {
		var row []discord.InteractiveComponent
		for ci, c := range q.Choices {
			row = append(row, discord.NewPrimaryButton(Truncate(c, labelLimit), id(KindChoice, ci)))
			if len(row) == buttonsPerRow {
				rows, row = append(rows, row), nil
			}
		}
		if len(row) > 0 {
			rows = append(rows, row)
		}
	}
	other := "Other…"
	if len(q.Choices) == 0 {
		other = "Answer…"
	}
	rows = append(rows, []discord.InteractiveComponent{discord.NewSecondaryButton(other, id(KindOther, 0))})
	return rows
}

// ApprovalMessage renders a tool approval request.
func ApprovalMessage(agent, taskID string, req *kagent.Request, notify []snowflake.ID) discord.MessageCreate {
	ping, allowed := Ping(notify)
	var b strings.Builder
	b.WriteString(ping)
	fmt.Fprintf(&b, "**%s** wants to run", agent)
	if req.Subagent != "" {
		fmt.Fprintf(&b, " (through %s)", req.Subagent)
	}
	b.WriteString(":\n")
	if req.Hint != "" {
		b.WriteString(quote(req.Hint))
		b.WriteString("\n")
	}
	for i, tc := range req.Tools {
		fmt.Fprintf(&b, "**%d.** `%s`", i+1, tc.Name)
		if len(tc.Args) > 0 {
			args, _ := json.MarshalIndent(tc.Args, "", "  ")
			fmt.Fprintf(&b, "\n```json\n%s\n```", Truncate(string(args), argsLimit))
		}
		b.WriteString("\n")
	}
	m := discord.NewMessageCreate().
		WithContent(Truncate(strings.TrimSpace(b.String()), MessageLimit)).
		WithAllowedMentions(allowed)
	id := func(kind Kind, i int) string {
		return ID{Kind: kind, Task: taskID, Key: ShortKey(req.Key()), Index: i}.String()
	}
	if len(req.Tools) <= maxButtonRows {
		for i, tc := range req.Tools {
			m = m.AddActionRow(
				discord.NewSuccessButton(Truncate("Approve "+tc.Name, labelLimit), id(KindApprove, i)),
				discord.NewDangerButton("Reject…", id(KindReject, i)),
			)
		}
	}
	if len(req.Tools) > 1 {
		m = m.AddActionRow(
			discord.NewSuccessButton("Approve all", id(KindApproveAll, 0)),
			discord.NewDangerButton("Reject all…", id(KindRejectAll, 0)),
		)
	}
	return m
}

// AnswerModal asks for a free-text answer. id is the submit ID.
func AnswerModal(id ID, question string) discord.ModalCreate {
	return discord.NewModalCreate(id.String(), "Answer").
		AddLabel(Truncate(question, modalTitleLimit),
			discord.NewParagraphTextInput(TextInputID).WithRequired(true).WithMaxLength(4000))
}

// RejectModal asks for an optional rejection reason. id is the submit ID.
func RejectModal(id ID, what string) discord.ModalCreate {
	return discord.NewModalCreate(id.String(), Truncate("Reject "+what, modalTitleLimit)).
		AddLabel("Reason (optional)",
			discord.NewParagraphTextInput(TextInputID).WithRequired(false).WithMaxLength(1000))
}

// Answered is a request message after it was answered: the same text, no
// components, and a line saying what was chosen, by whom, and an optional
// note.
func Answered(content, summary string, by snowflake.ID, note string) discord.MessageUpdate {
	line := fmt.Sprintf("\n-# ↳ %s by <@%s>", summary, by)
	if note != "" {
		line += " — " + note
	}
	content = Truncate(content, MessageLimit-len([]rune(line))) + line
	return discord.NewMessageUpdate().
		WithContent(content).
		ClearComponents().
		WithAllowedMentions(&discord.AllowedMentions{})
}

func quote(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, l := range lines {
		lines[i] = "> " + l
	}
	return strings.Join(lines, "\n")
}

// QuestionIn recovers the question from a message QuestionMessages made, so
// a modal can be opened without asking kagent first: Discord gives the bot
// three seconds to answer an interaction.
func QuestionIn(content string) string {
	var lines []string
	for line := range strings.SplitSeq(content, "\n") {
		if q, ok := strings.CutPrefix(line, "> "); ok {
			lines = append(lines, q)
		} else if len(lines) > 0 {
			break
		}
	}
	if len(lines) == 0 {
		return "Your answer"
	}
	return strings.Join(lines, " ")
}

// ToolIn recovers the name of tool i from a message ApprovalMessage made.
func ToolIn(content string, i int) string {
	prefix := fmt.Sprintf("**%d.** `", i+1)
	for line := range strings.SplitSeq(content, "\n") {
		if rest, ok := strings.CutPrefix(line, prefix); ok {
			if name, _, ok := strings.Cut(rest, "`"); ok {
				return name
			}
		}
	}
	return "this call"
}
