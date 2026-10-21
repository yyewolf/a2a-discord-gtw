// Package kagent talks A2A v1 to kagent agents, including kagent's
// human-in-the-loop extension.
package kagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// HITLExtension is kagent's human-in-the-loop A2A extension. A paused task
// carries its request in the status message metadata under this key, and the
// answer goes back in the same place on a message to the same task.
const HITLExtension = "https://kagent.dev/extensions/hitl/v1"

const (
	typeToolApprovalRequest  = "tool_approval_request"
	typeAskUserRequest       = "ask_user_request"
	typeToolApprovalResponse = "tool_approval_response"
	typeAskUserResponse      = "ask_user_response"
)

// Question is one ask-user question. Choices may be empty, in which case
// only a free-text answer is possible.
type Question struct {
	Question string   `json:"question"`
	Choices  []string `json:"choices"`
	Multiple bool     `json:"multiple"`
}

// ToolCall is one tool invocation waiting for approval.
type ToolCall struct {
	ID     string         `json:"id"`
	CallID string         `json:"call_id"`
	Name   string         `json:"name"`
	Args   map[string]any `json:"args"`
}

// Request is what a paused task is waiting for: questions or tool approvals.
// Exactly one of Questions and Tools is non-empty.
type Request struct {
	// ID correlates an ask-user answer with its request. Tool approvals have
	// no request ID; their tools' IDs play that role.
	ID        string
	Questions []Question
	Hint      string
	Tools     []ToolCall
	// Subagent names the child agent a nested approval came from, if any.
	Subagent string
}

// IsApproval reports whether the request is a tool approval.
func (r *Request) IsApproval() bool { return len(r.Tools) > 0 }

// Key identifies the request for staleness checks: the ask-user ID, or the
// first tool's ID for approvals.
func (r *Request) Key() string {
	if r.IsApproval() {
		return r.Tools[0].ID
	}
	return r.ID
}

type rawRequest struct {
	Type      string     `json:"type"`
	ID        string     `json:"id"`
	Hint      string     `json:"hint"`
	Questions []Question `json:"questions"`
	Tools     []ToolCall `json:"tools"`
	Nested    *struct {
		SubagentName string     `json:"subagent_name"`
		Tools        []ToolCall `json:"tools"`
	} `json:"nested"`
}

// ErrNoRequest means the task is not waiting for a human.
var ErrNoRequest = errors.New("kagent: task is not waiting for input")

// PendingRequest extracts the HITL request from a task waiting for input.
// The status message's text part may be empty, so the request is read from
// metadata, never from text.
func PendingRequest(t *a2a.Task) (*Request, error) {
	if t == nil || t.Status.State != a2a.TaskStateInputRequired || t.Status.Message == nil {
		return nil, ErrNoRequest
	}
	return RequestFromMessage(t.Status.Message)
}

// RequestFromMessage extracts the HITL request from a status message.
func RequestFromMessage(m *a2a.Message) (*Request, error) {
	if m == nil {
		return nil, ErrNoRequest
	}
	payload, ok := m.Metadata[HITLExtension]
	if !ok {
		return nil, ErrNoRequest
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("kagent: encode hitl payload: %w", err)
	}
	var raw rawRequest
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("kagent: decode hitl payload: %w", err)
	}
	switch raw.Type {
	case typeAskUserRequest:
		if raw.ID == "" || len(raw.Questions) == 0 {
			return nil, fmt.Errorf("kagent: ask_user_request without id or questions")
		}
		return &Request{ID: raw.ID, Questions: raw.Questions}, nil
	case typeToolApprovalRequest:
		r := &Request{Hint: raw.Hint, Tools: raw.Tools}
		if raw.Nested != nil {
			r.Subagent = raw.Nested.SubagentName
			if len(r.Tools) == 0 {
				r.Tools = raw.Nested.Tools
			}
		}
		if len(r.Tools) == 0 {
			return nil, fmt.Errorf("kagent: tool_approval_request without tools")
		}
		return r, nil
	}
	return nil, fmt.Errorf("kagent: unknown hitl request type %q", raw.Type)
}

// Approval is one decision on a tool call.
type Approval struct {
	ID              string `json:"id"`
	Approved        bool   `json:"approved"`
	RejectionReason string `json:"rejection_reason,omitempty"`
}

type askUserAnswer struct {
	Answer []string `json:"answer"`
}

type askUserResponse struct {
	Type    string          `json:"type"`
	ID      string          `json:"id"`
	Answers []askUserAnswer `json:"answers"`
}

type toolApprovalResponse struct {
	Type      string     `json:"type"`
	Approvals []Approval `json:"approvals"`
}

// AnswerMessage builds the reply to an ask-user request: one answer per
// question, in order. Each answer is wrapped as {"answer": [...]}; kagent
// accepts a bare [[...]] too, fails to decode it later, and leaves the task
// stuck in WORKING, so the shape is checked here.
func AnswerMessage(t *a2a.Task, r *Request, answers [][]string, text string) (*a2a.Message, error) {
	if r.IsApproval() {
		return nil, errors.New("kagent: request is a tool approval, not a question")
	}
	if len(answers) != len(r.Questions) {
		return nil, fmt.Errorf("kagent: %d answers for %d questions", len(answers), len(r.Questions))
	}
	resp := askUserResponse{Type: typeAskUserResponse, ID: r.ID}
	for i, a := range answers {
		if len(a) == 0 {
			return nil, fmt.Errorf("kagent: question %d has no answer", i+1)
		}
		if !r.Questions[i].Multiple && len(a) != 1 {
			return nil, fmt.Errorf("kagent: question %d takes one answer, got %d", i+1, len(a))
		}
		resp.Answers = append(resp.Answers, askUserAnswer{Answer: a})
	}
	return hitlMessage(t, resp, text)
}

// ApprovalMessage builds the reply to a tool approval request: one decision
// per requested tool.
func ApprovalMessage(t *a2a.Task, r *Request, approvals []Approval, text string) (*a2a.Message, error) {
	if !r.IsApproval() {
		return nil, errors.New("kagent: request is a question, not a tool approval")
	}
	want := map[string]bool{}
	for _, tc := range r.Tools {
		want[tc.ID] = true
	}
	got := map[string]bool{}
	for _, a := range approvals {
		if !want[a.ID] || got[a.ID] {
			return nil, fmt.Errorf("kagent: unexpected or duplicate decision for tool %q", a.ID)
		}
		got[a.ID] = true
	}
	if len(got) != len(want) {
		return nil, fmt.Errorf("kagent: %d decisions for %d tools", len(got), len(want))
	}
	return hitlMessage(t, toolApprovalResponse{Type: typeToolApprovalResponse, Approvals: approvals}, text)
}

func hitlMessage(t *a2a.Task, payload any, text string) (*a2a.Message, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	m := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(text))
	m.TaskID = t.ID
	m.ContextID = t.ContextID
	m.Metadata = map[string]any{HITLExtension: raw}
	if !slices.Contains(m.Extensions, HITLExtension) {
		m.Extensions = append(m.Extensions, HITLExtension)
	}
	return m, nil
}
