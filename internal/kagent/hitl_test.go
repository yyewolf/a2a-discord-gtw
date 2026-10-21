package kagent_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/yyewolf/a2a-discord-gtw/internal/kagent"
)

// askUserTask is a task as kagent 1.0.0-alpha17 returned it for a Claude
// harness AskUserQuestion: input-required, the question only in metadata,
// and an empty text part.
const askUserTask = `{
  "id": "01a15fd3-28b5-7a83-8cb1-60f4d69989fe",
  "contextId": "01a15fd3-271b-744f-85db-03cc2153969c",
  "status": {
    "state": "TASK_STATE_INPUT_REQUIRED",
    "message": {
      "messageId": "m1",
      "role": "ROLE_AGENT",
      "parts": [{"text": ""}],
      "metadata": {
        "https://kagent.dev/extensions/hitl/v1": {
          "id": "156cdfea-eb43-4470-bd78-0d5d369497bd",
          "questions": [{"choices": ["Red", "Blue"], "multiple": false, "question": "Which color do you prefer?"}],
          "type": "ask_user_request"
        },
        "kagent.dev/a2a/timeline-position": "2026-10-21T18:57:20.029582701Z"
      }
    }
  }
}`

const approvalTask = `{
  "id": "t2",
  "contextId": "c2",
  "status": {
    "state": "TASK_STATE_INPUT_REQUIRED",
    "message": {
      "messageId": "m2",
      "role": "ROLE_AGENT",
      "parts": [{"text": "Approval is required for tool(s): delete_file"}],
      "metadata": {
        "https://kagent.dev/extensions/hitl/v1": {
          "type": "tool_approval_request",
          "hint": "Allow these calls?",
          "tools": [
            {"id": "tool-1", "call_id": "call-1", "name": "delete_file", "args": {"path": "/tmp/x"}},
            {"id": "tool-2", "call_id": "call-2", "name": "restart_pod", "args": {}}
          ]
        }
      }
    }
  }
}`

func decodeTask(t *testing.T, s string) *a2a.Task {
	t.Helper()
	var task a2a.Task
	if err := json.Unmarshal([]byte(s), &task); err != nil {
		t.Fatal(err)
	}
	return &task
}

func TestPendingAskUser(t *testing.T) {
	r, err := kagent.PendingRequest(decodeTask(t, askUserTask))
	if err != nil {
		t.Fatal(err)
	}
	if r.IsApproval() || r.ID != "156cdfea-eb43-4470-bd78-0d5d369497bd" || r.Key() != r.ID {
		t.Fatalf("request = %+v", r)
	}
	if len(r.Questions) != 1 || r.Questions[0].Question != "Which color do you prefer?" || len(r.Questions[0].Choices) != 2 {
		t.Fatalf("questions = %+v", r.Questions)
	}
}

func TestPendingApproval(t *testing.T) {
	r, err := kagent.PendingRequest(decodeTask(t, approvalTask))
	if err != nil {
		t.Fatal(err)
	}
	if !r.IsApproval() || r.Hint != "Allow these calls?" || len(r.Tools) != 2 || r.Key() != "tool-1" {
		t.Fatalf("request = %+v", r)
	}
	if r.Tools[0].Args["path"] != "/tmp/x" {
		t.Fatalf("args = %v", r.Tools[0].Args)
	}
}

func TestPendingRequiresInputState(t *testing.T) {
	task := decodeTask(t, askUserTask)
	task.Status.State = a2a.TaskStateWorking
	if _, err := kagent.PendingRequest(task); !errors.Is(err, kagent.ErrNoRequest) {
		t.Fatalf("err = %v", err)
	}
}

func TestAnswerMessageShape(t *testing.T) {
	task := decodeTask(t, askUserTask)
	r, _ := kagent.PendingRequest(task)
	m, err := kagent.AnswerMessage(task, r, [][]string{{"Blue"}}, "Blue")
	if err != nil {
		t.Fatal(err)
	}
	if m.TaskID != task.ID || m.ContextID != task.ContextID {
		t.Fatalf("ids = %s/%s", m.TaskID, m.ContextID)
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	// The verified wire shape. [["Blue"]] would be accepted by kagent and then
	// leave the task stuck in WORKING.
	for _, want := range []string{
		`"answers":[{"answer":["Blue"]}]`,
		`"id":"156cdfea-eb43-4470-bd78-0d5d369497bd"`,
		`"type":"ask_user_response"`,
		`"extensions":["https://kagent.dev/extensions/hitl/v1"]`,
		`"taskId":"01a15fd3-28b5-7a83-8cb1-60f4d69989fe"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("message %s lacks %s", s, want)
		}
	}
}

func TestAnswerMessageValidates(t *testing.T) {
	task := decodeTask(t, askUserTask)
	r, _ := kagent.PendingRequest(task)
	for name, answers := range map[string][][]string{
		"too few":       {},
		"empty answer":  {{}},
		"two on single": {{"Red", "Blue"}},
	} {
		if _, err := kagent.AnswerMessage(task, r, answers, ""); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestApprovalMessage(t *testing.T) {
	task := decodeTask(t, approvalTask)
	r, _ := kagent.PendingRequest(task)
	if _, err := kagent.ApprovalMessage(task, r, []kagent.Approval{{ID: "tool-1", Approved: true}}, ""); err == nil {
		t.Fatal("expected an error for a missing decision")
	}
	m, err := kagent.ApprovalMessage(task, r, []kagent.Approval{
		{ID: "tool-1", Approved: true},
		{ID: "tool-2", Approved: false, RejectionReason: "not now"},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(m)
	if !strings.Contains(string(b), `"approvals":[{"approved":true,"id":"tool-1"},{"approved":false,"id":"tool-2","rejection_reason":"not now"}]`) {
		t.Fatalf("message = %s", b)
	}
}
