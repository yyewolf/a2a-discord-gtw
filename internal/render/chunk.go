// Package render turns agent output and requests into Discord messages.
package render

import "strings"

// MessageLimit is Discord's maximum message length.
const MessageLimit = 2000

// Chunk splits text into pieces of at most limit characters (runes), breaking
// at a paragraph, then a line, then a space, and only then mid-word. A code
// fence open at a break is closed and reopened in the next piece, so every
// piece renders on its own.
func Chunk(text string, limit int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	var out []string
	reopen := ""
	for text != "" {
		budget := limit - len([]rune(reopen))
		body, sep := text, ""
		if len([]rune(reopen))+len([]rune(text)) > limit {
			// Leave room to close a fence that may be open at the break.
			body, sep = cut(text, budget-len("\n```"))
		}
		// Drop only the separator broken at: leading spaces of the next
		// piece may be code indentation.
		text = strings.TrimPrefix(text[len(body):], sep)
		piece := reopen + body
		fence, open := openFence(piece)
		reopen = ""
		if open && text != "" {
			piece += "\n```"
			reopen = fence + "\n"
		}
		out = append(out, strings.TrimSpace(piece))
	}
	return out
}

// cut returns the longest prefix of s within n runes that ends at the best
// available boundary, and the separator found there ("" for a hard cut).
func cut(s string, n int) (string, string) {
	if n < 1 {
		n = 1
	}
	r := []rune(s)
	if len(r) <= n {
		return s, ""
	}
	head := string(r[:n])
	for _, sep := range []string{"\n\n", "\n", " "} {
		if i := strings.LastIndex(head, sep); i > len(head)/2 {
			return head[:i], sep
		}
	}
	return head, ""
}

// openFence reports whether s ends inside a ``` block, and the fence line
// (with its language) that opened it.
func openFence(s string) (string, bool) {
	fence, open := "", false
	for line := range strings.SplitSeq(s, "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "```") {
			continue
		}
		if open {
			open = false
			continue
		}
		fence, open = t, true
	}
	return fence, open
}
