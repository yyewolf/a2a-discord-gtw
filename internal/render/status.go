package render

import (
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

const titleLimit = 90

// Title is a conversation's one-line title: the first line of its prompt,
// shortened.
func Title(prompt string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(prompt), "\n")
	line = strings.Join(strings.Fields(line), " ")
	if line == "" {
		return "conversation"
	}
	return Truncate(line, titleLimit)
}

// ThreadName is a title within Discord's 100-character thread name limit.
func ThreadName(title string) string { return Truncate(title, 100) }

// Truncate shortens s to at most n runes, ending with an ellipsis if cut.
func Truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// StateMark is the emoji shown for a task state.
func StateMark(s a2a.TaskState) string {
	switch s {
	case a2a.TaskStateInputRequired, a2a.TaskStateAuthRequired:
		return "🙋"
	case a2a.TaskStateCompleted:
		return "✅"
	case a2a.TaskStateFailed, a2a.TaskStateRejected:
		return "❌"
	case a2a.TaskStateCanceled:
		return "⛔"
	default:
		return "⏳"
	}
}

func stateWord(s a2a.TaskState) string {
	switch s {
	case a2a.TaskStateInputRequired, a2a.TaskStateAuthRequired:
		return "needs input"
	case a2a.TaskStateCompleted:
		return "done"
	case a2a.TaskStateFailed:
		return "failed"
	case a2a.TaskStateRejected:
		return "rejected"
	case a2a.TaskStateCanceled:
		return "canceled"
	default:
		return "working"
	}
}

// StatusLine is the content of a conversation's top-level message, edited as
// its latest task changes state. origin says who started it, e.g. a user
// mention, or "scheduled" for a task the bot found.
func StatusLine(agent, title, origin string, s a2a.TaskState) string {
	var b strings.Builder
	b.WriteString(StateMark(s))
	b.WriteString(" **")
	b.WriteString(agent)
	b.WriteString("** · ")
	b.WriteString(stateWord(s))
	if origin != "" {
		b.WriteString(" · ")
		b.WriteString(origin)
	}
	b.WriteString("\n> ")
	b.WriteString(title)
	return b.String()
}
