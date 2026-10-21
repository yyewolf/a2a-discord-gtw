// Package kagenttest is a fake kagent A2A v1 JSON-RPC endpoint for tests. It
// reproduces the shapes kagent 1.0.0-alpha17 was observed to return.
package kagenttest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// WaitingError is kagent's answer to a new turn on a context whose task is
// waiting for input.
var WaitingError = &RPCError{Code: -32004, Message: "native session is waiting for another task"}

// RPCError is a JSON-RPC error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Call records one JSON-RPC request.
type Call struct {
	Method string
	Header http.Header
	Params json.RawMessage
}

// SendFunc decides what a SendMessage/SendStreamingMessage produces. The last
// event should be the resulting task, as kagent ends its streams.
type SendFunc func(msg *a2a.Message) ([]a2a.Event, *RPCError)

// Server is a fake agent endpoint at URL (the agent's A2A URL).
type Server struct {
	*httptest.Server
	Card a2a.AgentCard

	mu    sync.Mutex
	tasks map[a2a.TaskID]*a2a.Task
	order []a2a.TaskID
	calls []Call
	send  SendFunc
}

// New starts a fake agent.
func New() *Server {
	s := &Server{tasks: map[a2a.TaskID]*a2a.Task{}}
	s.Card = a2a.AgentCard{Name: "fake", Description: "A fake agent."}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// OnSend sets the behaviour for sent messages.
func (s *Server) OnSend(f SendFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.send = f
}

// PutTask stores or replaces a task, as if the agent had run it.
func (s *Server) PutTask(t *a2a.Task) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.putLocked(t)
}

func (s *Server) putLocked(t *a2a.Task) {
	if t.Status.Timestamp == nil {
		now := time.Now().UTC()
		t.Status.Timestamp = &now
	}
	if _, ok := s.tasks[t.ID]; !ok {
		s.order = append(s.order, t.ID)
	}
	cp := *t
	s.tasks[t.ID] = &cp
}

// Calls returns the requests received so far.
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Call(nil), s.calls...)
}

// LastCall returns the most recent call with the given method.
func (s *Server) LastCall(method string) (Call, bool) {
	calls := s.Calls()
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].Method == method {
			return calls[i], true
		}
	}
	return Call{}, false
}

type request struct {
	ID     any             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/.well-known/agent-card.json") {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s.Card)
		return
	}
	var req request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.calls = append(s.calls, Call{Method: req.Method, Header: r.Header.Clone(), Params: req.Params})
	s.mu.Unlock()

	switch req.Method {
	case "SendMessage", "SendStreamingMessage":
		var p a2a.SendMessageRequest
		if err := json.Unmarshal(req.Params, &p); err != nil {
			writeError(w, req.ID, &RPCError{Code: -32602, Message: err.Error()})
			return
		}
		s.mu.Lock()
		send := s.send
		s.mu.Unlock()
		if send == nil {
			writeError(w, req.ID, &RPCError{Code: -32603, Message: "fake: no OnSend"})
			return
		}
		events, rpcErr := send(p.Message)
		if rpcErr != nil {
			if req.Method == "SendStreamingMessage" {
				// a2a-go servers report streaming failures as an SSE event.
				writeStreamError(w, req.ID, rpcErr)
				return
			}
			writeError(w, req.ID, rpcErr)
			return
		}
		for _, ev := range events {
			if t, ok := ev.(*a2a.Task); ok {
				s.PutTask(t)
			}
		}
		if req.Method == "SendMessage" {
			writeResult(w, req.ID, a2a.StreamResponse{Event: events[len(events)-1]})
			return
		}
		writeStream(w, req.ID, events)
	case "GetTask":
		var p a2a.GetTaskRequest
		_ = json.Unmarshal(req.Params, &p)
		s.mu.Lock()
		t, ok := s.tasks[p.ID]
		s.mu.Unlock()
		if !ok {
			writeError(w, req.ID, &RPCError{Code: -32001, Message: "task not found"})
			return
		}
		writeResult(w, req.ID, t)
	case "ListTasks":
		var p a2a.ListTasksRequest
		_ = json.Unmarshal(req.Params, &p)
		s.mu.Lock()
		var out []*a2a.Task
		for _, id := range s.order {
			t := s.tasks[id]
			if p.StatusTimestampAfter != nil && !t.Status.Timestamp.After(*p.StatusTimestampAfter) {
				continue
			}
			if p.ContextID != "" && t.ContextID != p.ContextID {
				continue
			}
			out = append(out, t)
		}
		s.mu.Unlock()
		writeResult(w, req.ID, a2a.ListTasksResponse{Tasks: out, TotalSize: len(out), PageSize: len(out)})
	case "CancelTask":
		var p a2a.CancelTaskRequest
		_ = json.Unmarshal(req.Params, &p)
		s.mu.Lock()
		t, ok := s.tasks[p.ID]
		if ok {
			t.Status.State = a2a.TaskStateCanceled
		}
		s.mu.Unlock()
		if !ok {
			writeError(w, req.ID, &RPCError{Code: -32001, Message: "task not found"})
			return
		}
		writeResult(w, req.ID, t)
	default:
		writeError(w, req.ID, &RPCError{Code: -32601, Message: "method not found: " + req.Method})
	}
}

func writeResult(w http.ResponseWriter, id any, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func writeError(w http.ResponseWriter, id any, e *RPCError) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "error": e})
}

func writeStreamError(w http.ResponseWriter, id any, e *RPCError) {
	w.Header().Set("Content-Type", "text/event-stream")
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": e})
	_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
}

func writeStream(w http.ResponseWriter, id any, events []a2a.Event) {
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	for _, ev := range events {
		b, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": a2a.StreamResponse{Event: ev}})
		if err != nil {
			panic(fmt.Sprintf("kagenttest: marshal event: %v", err))
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
		if flusher != nil {
			flusher.Flush()
		}
	}
}
