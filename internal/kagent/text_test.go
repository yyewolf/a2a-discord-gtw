package kagent_test

import (
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/yyewolf/a2a-discord-gtw/internal/kagent"
)

func artifact(id, text string) *a2a.Artifact {
	return &a2a.Artifact{ID: a2a.ArtifactID(id), Parts: a2a.ContentParts{a2a.NewTextPart(text)}}
}

func TestTaskTextConcatenatesArtifacts(t *testing.T) {
	// The shape kagent returned after an answered question: empty artifacts
	// from before the pause, then the reply.
	task := &a2a.Task{Artifacts: []*a2a.Artifact{artifact("a", ""), artifact("b", ""), artifact("c", " YOU PICKED Blue.")}}
	if got := kagent.TaskText(task); got != "YOU PICKED Blue." {
		t.Fatalf("text = %q", got)
	}
	task = &a2a.Task{Artifacts: []*a2a.Artifact{artifact("a", " Here"), artifact("b", " is the"), artifact("c", " result")}}
	if got := kagent.TaskText(task); got != "Here is the result" {
		t.Fatalf("text = %q", got)
	}
}

func TestTaskTextFallsBackToStatus(t *testing.T) {
	task := &a2a.Task{Status: a2a.TaskStatus{State: a2a.TaskStateFailed, Message: a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("boom"))}}
	if got := kagent.TaskText(task); got != "boom" {
		t.Fatalf("text = %q", got)
	}
}

func TestTranscript(t *testing.T) {
	var tr kagent.Transcript
	tr.Add(&a2a.TaskArtifactUpdateEvent{Artifact: artifact("a", "Hel")})
	tr.Add(&a2a.TaskArtifactUpdateEvent{Artifact: artifact("a", "lo"), Append: true})
	tr.Add(&a2a.TaskArtifactUpdateEvent{Artifact: artifact("b", " world")})
	tr.Add(&a2a.TaskArtifactUpdateEvent{Artifact: artifact("b", " there"), Append: false})
	tr.Add(nil)
	if got := tr.Text(); got != "Hello there" {
		t.Fatalf("text = %q", got)
	}
}
