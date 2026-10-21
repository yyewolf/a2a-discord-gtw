package store

import (
	"context"
	"encoding/json"
	"fmt"
)

// SetPartial records the answer to one question (or the decision on one
// tool) of a pending request.
func (s *Store) SetPartial(ctx context.Context, taskID, requestKey string, idx int, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("store: encode partial answer: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO partial_answers (task_id, request_key, idx, value) VALUES (?, ?, ?, ?)
		 ON CONFLICT (task_id, request_key, idx) DO UPDATE SET value = excluded.value`,
		taskID, requestKey, idx, string(b))
	if err != nil {
		return fmt.Errorf("store: set partial answer: %w", err)
	}
	return nil
}

// Partials returns the raw answers recorded for a request, by index.
func (s *Store) Partials(ctx context.Context, taskID, requestKey string) (map[int]json.RawMessage, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT idx, value FROM partial_answers WHERE task_id = ? AND request_key = ?`, taskID, requestKey)
	if err != nil {
		return nil, fmt.Errorf("store: partial answers: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[int]json.RawMessage{}
	for rows.Next() {
		var idx int
		var v string
		if err := rows.Scan(&idx, &v); err != nil {
			return nil, fmt.Errorf("store: partial answers: %w", err)
		}
		out[idx] = json.RawMessage(v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: partial answers: %w", err)
	}
	return out, nil
}

// ClearPartials forgets every partial answer of a task once it is sent.
func (s *Store) ClearPartials(ctx context.Context, taskID string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM partial_answers WHERE task_id = ?`, taskID); err != nil {
		return fmt.Errorf("store: clear partial answers: %w", err)
	}
	return nil
}
