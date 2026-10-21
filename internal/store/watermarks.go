package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Watermark is the status timestamp up to which an agent's tasks have been
// seen by the watcher.
func (s *Store) Watermark(ctx context.Context, agent string) (time.Time, bool, error) {
	var at time.Time
	err := s.db.QueryRowContext(ctx, `SELECT at FROM watermarks WHERE agent = ?`, agent).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("store: watermark: %w", err)
	}
	return at, true, nil
}

// SetWatermark moves an agent's watermark.
func (s *Store) SetWatermark(ctx context.Context, agent string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO watermarks (agent, at) VALUES (?, ?)
		 ON CONFLICT (agent) DO UPDATE SET at = excluded.at`, agent, at.UTC())
	if err != nil {
		return fmt.Errorf("store: set watermark: %w", err)
	}
	return nil
}
