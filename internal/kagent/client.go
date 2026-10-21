package kagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"

	"github.com/yyewolf/a2a-discord-gtw/internal/buildinfo"
)

// Options configures every agent client.
type Options struct {
	// UserID is sent as X-User-Id; empty leaves kagent's default user.
	UserID string
	// HTTPClient defaults to a client with no overall timeout, since streams
	// stay open for as long as the agent works.
	HTTPClient *http.Client
}

// Agent is an A2A v1 client for one kagent agent.
type Agent struct {
	url    string
	userID string
	http   *http.Client
	client *a2aclient.Client
}

// NewAgent builds a client for the agent at url ({kagent}/agents/{ns}/{name}).
// It does not contact the agent.
func NewAgent(ctx context.Context, url string, opts Options) (*Agent, error) {
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Transport: userAgentTransport{base: http.DefaultTransport}}
	}
	c, err := a2aclient.NewFromEndpoints(ctx,
		[]*a2a.AgentInterface{a2a.NewAgentInterface(url, a2a.TransportProtocolJSONRPC)},
		a2aclient.WithJSONRPCTransport(hc),
	)
	if err != nil {
		return nil, fmt.Errorf("kagent: client for %s: %w", url, err)
	}
	return &Agent{url: url, userID: opts.UserID, http: hc, client: c}, nil
}

type userAgentTransport struct{ base http.RoundTripper }

func (t userAgentTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("User-Agent", buildinfo.UserAgent())
	return t.base.RoundTrip(r)
}

// ctx attaches the headers every call carries: the user, and the HITL
// extension so paused tasks carry their structured request.
func (a *Agent) ctx(ctx context.Context) context.Context {
	params := a2aclient.ServiceParams{a2a.SvcParamExtensions: {HITLExtension}}
	if a.userID != "" {
		params["X-User-Id"] = []string{a.userID}
	}
	return a2aclient.AttachServiceParams(ctx, params)
}

// Card fetches the agent card, for its description.
func (a *Agent) Card(ctx context.Context) (*a2a.AgentCard, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(a.url, "/")+"/.well-known/agent-card.json", nil)
	if err != nil {
		return nil, err
	}
	res, err := a.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kagent: agent card: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kagent: agent card: status %d", res.StatusCode)
	}
	var card a2a.AgentCard
	if err := json.NewDecoder(res.Body).Decode(&card); err != nil {
		return nil, fmt.Errorf("kagent: agent card: %w", err)
	}
	return &card, nil
}

// TextMessage is a user turn. An empty contextID starts a new conversation.
func TextMessage(contextID, text string) *a2a.Message {
	m := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(text))
	m.ContextID = contextID
	return m
}

// Stream sends a message and yields the agent's events until the task
// settles (completes, fails, or pauses for input).
func (a *Agent) Stream(ctx context.Context, m *a2a.Message) iter.Seq2[a2a.Event, error] {
	return a.client.SendStreamingMessage(a.ctx(ctx), &a2a.SendMessageRequest{Message: m})
}

// Task fetches a task with its artifacts.
func (a *Agent) Task(ctx context.Context, id a2a.TaskID) (*a2a.Task, error) {
	t, err := a.client.GetTask(a.ctx(ctx), &a2a.GetTaskRequest{ID: id})
	if err != nil {
		return nil, fmt.Errorf("kagent: get task %s: %w", id, err)
	}
	return t, nil
}

// TasksSince lists tasks whose status changed after since, oldest first as
// kagent returns them, following pagination.
func (a *Agent) TasksSince(ctx context.Context, since time.Time) ([]*a2a.Task, error) {
	var out []*a2a.Task
	req := &a2a.ListTasksRequest{StatusTimestampAfter: &since, PageSize: 100}
	for range 50 {
		res, err := a.client.ListTasks(a.ctx(ctx), req)
		if err != nil {
			return nil, fmt.Errorf("kagent: list tasks: %w", err)
		}
		out = append(out, res.Tasks...)
		if res.NextPageToken == "" {
			return out, nil
		}
		req.PageToken = res.NextPageToken
	}
	return out, errors.New("kagent: list tasks: too many pages")
}

// PendingTask returns the task of a context that is waiting for input, or
// ErrNoRequest if there is none.
func (a *Agent) PendingTask(ctx context.Context, contextID string) (*a2a.Task, error) {
	res, err := a.client.ListTasks(a.ctx(ctx), &a2a.ListTasksRequest{ContextID: contextID, Status: a2a.TaskStateInputRequired, PageSize: 10})
	if err != nil {
		return nil, fmt.Errorf("kagent: list pending tasks: %w", err)
	}
	for _, t := range res.Tasks {
		// Filter here too, in case the server ignores the status filter.
		if t.ContextID == contextID && t.Status.State == a2a.TaskStateInputRequired {
			return a.Task(ctx, t.ID)
		}
	}
	return nil, ErrNoRequest
}

// Cancel cancels a task.
func (a *Agent) Cancel(ctx context.Context, id a2a.TaskID) error {
	if _, err := a.client.CancelTask(a.ctx(ctx), &a2a.CancelTaskRequest{ID: id}); err != nil {
		return fmt.Errorf("kagent: cancel task %s: %w", id, err)
	}
	return nil
}

// Close releases the client.
func (a *Agent) Close() error { return a.client.Destroy() }

// IsWaitingForInput reports kagent's refusal of a new turn on a context whose
// task is paused for input. The pending question has to be answered instead.
func IsWaitingForInput(err error) bool {
	return errors.Is(err, a2a.ErrUnsupportedOperation) && strings.Contains(err.Error(), "waiting for another task")
}
