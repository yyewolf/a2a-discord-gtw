package store

import (
	"context"
	"fmt"
)

// RequestMessage is a Discord message carrying a request's components.
type RequestMessage struct {
	ChannelID uint64
	MessageID uint64
}

// AddRequestMessage records a message posted for a task's pending request.
func (s *Store) AddRequestMessage(ctx context.Context, taskID string, m RequestMessage) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO request_messages (task_id, channel_id, message_id) VALUES (?, ?, ?)`,
		taskID, id64(m.ChannelID), id64(m.MessageID))
	if err != nil {
		return fmt.Errorf("store: add request message: %w", err)
	}
	return nil
}

// TakeRequestMessages returns and forgets the request messages of a task.
func (s *Store) TakeRequestMessages(ctx context.Context, taskID string) ([]RequestMessage, error) {
	rows, err := s.db.QueryContext(ctx,
		`DELETE FROM request_messages WHERE task_id = ? RETURNING channel_id, message_id`, taskID)
	if err != nil {
		return nil, fmt.Errorf("store: take request messages: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []RequestMessage
	for rows.Next() {
		var ch, msg int64
		if err := rows.Scan(&ch, &msg); err != nil {
			return nil, fmt.Errorf("store: take request messages: %w", err)
		}
		out = append(out, RequestMessage{ChannelID: uid(ch), MessageID: uid(msg)})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: take request messages: %w", err)
	}
	return out, nil
}
