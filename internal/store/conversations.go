package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Conversation is a Discord thread bound to one agent's A2A context. The
// thread hangs off StatusMessageID in ChannelID, the agent's channel.
type Conversation struct {
	ThreadID        uint64
	Agent           string
	ContextID       string
	ChannelID       uint64
	StatusMessageID uint64
	Title           string
	// OwnStatus is true when the status message is the bot's own, so the
	// state is shown by editing it; otherwise by a reaction on it.
	OwnStatus bool
	// State is the A2A task state last shown.
	State string
	// Origin says who started it, e.g. a user mention or "scheduled".
	Origin    string
	CreatedAt time.Time
}

// PutConversation records a conversation. A context already bound to a
// thread keeps its first thread.
func (s *Store) PutConversation(ctx context.Context, c Conversation) error {
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO conversations (thread_id, agent, context_id, channel_id, status_message_id, title, own_status, state, origin, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id64(c.ThreadID), c.Agent, c.ContextID, id64(c.ChannelID), id64(c.StatusMessageID), c.Title, c.OwnStatus, c.State, c.Origin, c.CreatedAt)
	if err != nil {
		return fmt.Errorf("store: put conversation: %w", err)
	}
	return nil
}

// ConversationByThread finds the conversation of a Discord thread.
func (s *Store) ConversationByThread(ctx context.Context, threadID uint64) (Conversation, bool, error) {
	return s.conversation(ctx, `WHERE thread_id = ?`, id64(threadID))
}

// ConversationByContext finds the thread of an agent's A2A context.
func (s *Store) ConversationByContext(ctx context.Context, agent, contextID string) (Conversation, bool, error) {
	return s.conversation(ctx, `WHERE agent = ? AND context_id = ?`, agent, contextID)
}

func (s *Store) conversation(ctx context.Context, where string, args ...any) (Conversation, bool, error) {
	var c Conversation
	var thread, channel, status int64
	err := s.db.QueryRowContext(ctx,
		`SELECT thread_id, agent, context_id, channel_id, status_message_id, title, own_status, state, origin, created_at
		 FROM conversations `+where, args...).
		Scan(&thread, &c.Agent, &c.ContextID, &channel, &status, &c.Title, &c.OwnStatus, &c.State, &c.Origin, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Conversation{}, false, nil
	}
	if err != nil {
		return Conversation{}, false, fmt.Errorf("store: conversation: %w", err)
	}
	c.ThreadID, c.ChannelID, c.StatusMessageID = uid(thread), uid(channel), uid(status)
	return c, true, nil
}

// SetConversationState records the state last shown for a conversation.
func (s *Store) SetConversationState(ctx context.Context, threadID uint64, state string) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE conversations SET state = ? WHERE thread_id = ?`, state, id64(threadID)); err != nil {
		return fmt.Errorf("store: set conversation state: %w", err)
	}
	return nil
}

// SQLite integers are signed; Discord IDs fit in 63 bits.
func id64(id uint64) int64 { return int64(id) } //nolint:gosec // see above

func uid(id int64) uint64 { return uint64(id) } //nolint:gosec // see above
