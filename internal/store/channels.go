package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// AgentChannel returns the Discord channel recorded for an agent.
func (s *Store) AgentChannel(ctx context.Context, agent string) (uint64, bool, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT channel_id FROM agent_channels WHERE agent = ?`, agent).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("store: agent channel: %w", err)
	}
	return uint64(id), true, nil //nolint:gosec // Discord IDs fit in 63 bits
}

// SetAgentChannel records (or replaces) an agent's Discord channel.
func (s *Store) SetAgentChannel(ctx context.Context, agent string, channelID uint64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_channels (agent, channel_id) VALUES (?, ?)
		 ON CONFLICT (agent) DO UPDATE SET channel_id = excluded.channel_id`,
		agent, int64(channelID)) //nolint:gosec // Discord IDs fit in 63 bits
	if err != nil {
		return fmt.Errorf("store: set agent channel: %w", err)
	}
	return nil
}
