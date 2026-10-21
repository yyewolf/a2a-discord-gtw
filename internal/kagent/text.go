package kagent

import (
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// PartsText concatenates the text parts of a message or artifact.
func PartsText(parts a2a.ContentParts) string {
	var b strings.Builder
	for _, p := range parts {
		if p != nil {
			b.WriteString(p.Text())
		}
	}
	return b.String()
}

// TaskText is everything the agent has said in a task: kagent streams a reply
// as many small artifacts, so the text is their concatenation in order.
// Falls back to the status message when there are no artifacts.
func TaskText(t *a2a.Task) string {
	if t == nil {
		return ""
	}
	var b strings.Builder
	for _, a := range t.Artifacts {
		if a != nil {
			b.WriteString(PartsText(a.Parts))
		}
	}
	if b.Len() == 0 && t.Status.Message != nil {
		b.WriteString(PartsText(t.Status.Message.Parts))
	}
	return strings.TrimSpace(b.String())
}

// Transcript accumulates a streamed reply. Artifact updates either append to
// an artifact or replace it; the reply is all artifacts in arrival order.
type Transcript struct {
	order []a2a.ArtifactID
	text  map[a2a.ArtifactID]string
}

// Add applies one artifact update.
func (tr *Transcript) Add(ev *a2a.TaskArtifactUpdateEvent) {
	if ev == nil || ev.Artifact == nil {
		return
	}
	if tr.text == nil {
		tr.text = map[a2a.ArtifactID]string{}
	}
	id := ev.Artifact.ID
	prev, seen := tr.text[id]
	if !seen {
		tr.order = append(tr.order, id)
	}
	chunk := PartsText(ev.Artifact.Parts)
	if ev.Append {
		tr.text[id] = prev + chunk
	} else {
		tr.text[id] = chunk
	}
}

// Text is the reply so far.
func (tr *Transcript) Text() string {
	var b strings.Builder
	for _, id := range tr.order {
		b.WriteString(tr.text[id])
	}
	return strings.TrimSpace(b.String())
}
