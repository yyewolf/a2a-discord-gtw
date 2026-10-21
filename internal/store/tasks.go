package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// TaskRecord is what the bot has already shown for a task.
type TaskRecord struct {
	TaskID    string
	Agent     string
	ContextID string
	// State is the last A2A state shown on Discord.
	State string
	// PostedLen is how much of the task's reply text has been posted.
	PostedLen int
	// RequestKey is the HITL request last rendered, so a question is asked
	// once.
	RequestKey string
	UpdatedAt  time.Time
}

// Task returns the record of a task.
func (s *Store) Task(ctx context.Context, taskID string) (TaskRecord, bool, error) {
	var r TaskRecord
	err := s.db.QueryRowContext(ctx,
		`SELECT task_id, agent, context_id, state, posted_len, request_key, updated_at FROM tasks WHERE task_id = ?`, taskID).
		Scan(&r.TaskID, &r.Agent, &r.ContextID, &r.State, &r.PostedLen, &r.RequestKey, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return TaskRecord{}, false, nil
	}
	if err != nil {
		return TaskRecord{}, false, fmt.Errorf("store: task: %w", err)
	}
	return r, true, nil
}

// PutTask creates or replaces the record of a task.
func (s *Store) PutTask(ctx context.Context, r TaskRecord) error {
	r.UpdatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO tasks (task_id, agent, context_id, state, posted_len, request_key, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (task_id) DO UPDATE SET
		   state = excluded.state, posted_len = excluded.posted_len,
		   request_key = excluded.request_key, updated_at = excluded.updated_at`,
		r.TaskID, r.Agent, r.ContextID, r.State, r.PostedLen, r.RequestKey, r.UpdatedAt)
	if err != nil {
		return fmt.Errorf("store: put task: %w", err)
	}
	return nil
}

// UnsettledTasks are the recorded tasks not in a final state: still
// working, or waiting for input.
func (s *Store) UnsettledTasks(ctx context.Context) ([]TaskRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT task_id, agent, context_id, state, posted_len, request_key, updated_at FROM tasks
		 WHERE state NOT IN ('TASK_STATE_COMPLETED', 'TASK_STATE_FAILED', 'TASK_STATE_CANCELED', 'TASK_STATE_REJECTED')
		 ORDER BY updated_at`)
	if err != nil {
		return nil, fmt.Errorf("store: unsettled tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []TaskRecord
	for rows.Next() {
		var r TaskRecord
		if err := rows.Scan(&r.TaskID, &r.Agent, &r.ContextID, &r.State, &r.PostedLen, &r.RequestKey, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("store: unsettled tasks: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: unsettled tasks: %w", err)
	}
	return out, nil
}
