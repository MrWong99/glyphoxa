package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MrWong99/glyphoxa/pkg/memory"
)

// SessionStoreImpl is the L1 memory layer backed by a PostgreSQL
// session_entries table with a GIN full-text search index.
//
// Obtain one via [Store.L1] rather than constructing directly.
// All methods are safe for concurrent use.
type SessionStoreImpl struct {
	pool *pgxpool.Pool
}

// WriteEntry implements [memory.SessionStore]. It appends entry to the
// session_entries table under sessionID.
func (s *SessionStoreImpl) WriteEntry(ctx context.Context, sessionID string, entry memory.TranscriptEntry) error {
	const q = `
		INSERT INTO session_entries
		    (session_id, speaker_id, speaker_name, text, raw_text, npc_id, timestamp, duration_ns)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	_, err := s.pool.Exec(ctx, q,
		sessionID,
		entry.SpeakerID,
		entry.SpeakerName,
		entry.Text,
		entry.RawText,
		entry.NPCID,
		entry.Timestamp,
		entry.Duration.Nanoseconds(),
	)
	if err != nil {
		return fmt.Errorf("session store: write entry: %w", err)
	}
	return nil
}

// GetRecent implements [memory.SessionStore]. It returns all entries for
// sessionID whose timestamp is no earlier than time.Now()-duration, ordered
// chronologically (oldest first).
func (s *SessionStoreImpl) GetRecent(ctx context.Context, sessionID string, duration time.Duration) ([]memory.TranscriptEntry, error) {
	const q = `
		SELECT speaker_id, speaker_name, text, raw_text, npc_id, timestamp, duration_ns
		FROM   session_entries
		WHERE  session_id = $1
		  AND  timestamp  >= now() - ($2::bigint * interval '1 microsecond')
		ORDER  BY timestamp`

	rows, err := s.pool.Query(ctx, q, sessionID, duration.Microseconds())
	if err != nil {
		return nil, fmt.Errorf("session store: get recent: %w", err)
	}
	return collectEntries(rows)
}

// Search implements [memory.SessionStore]. It performs a PostgreSQL full-text
// search over the text column and applies optional filters from opts.
//
// The query is passed to plainto_tsquery so no special operator syntax is required.
func (s *SessionStoreImpl) Search(ctx context.Context, query string, opts memory.SearchOpts) ([]memory.TranscriptEntry, error) {
	args := []any{query} // $1 = FTS query string
	next := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	conditions := []string{
		"to_tsvector('english', text) @@ plainto_tsquery('english', $1)",
	}
	if opts.SessionID != "" {
		conditions = append(conditions, "session_id = "+next(opts.SessionID))
	}
	if !opts.After.IsZero() {
		conditions = append(conditions, "timestamp > "+next(opts.After))
	}
	if !opts.Before.IsZero() {
		conditions = append(conditions, "timestamp < "+next(opts.Before))
	}
	if opts.SpeakerID != "" {
		conditions = append(conditions, "speaker_id = "+next(opts.SpeakerID))
	}

	q := "SELECT speaker_id, speaker_name, text, raw_text, npc_id, timestamp, duration_ns\n" +
		"FROM   session_entries\n" +
		"WHERE  " + strings.Join(conditions, "\n  AND  ") + "\n" +
		"ORDER  BY timestamp"

	if opts.Limit > 0 {
		args = append(args, opts.Limit)
		q += fmt.Sprintf("\nLIMIT $%d", len(args))
	}

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("session store: search: %w", err)
	}
	return collectEntries(rows)
}

// collectEntries scans pgx rows into a slice of TranscriptEntry values.
func collectEntries(rows pgx.Rows) ([]memory.TranscriptEntry, error) {
	entries, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (memory.TranscriptEntry, error) {
		var (
			e          memory.TranscriptEntry
			durationNS int64
		)
		if err := row.Scan(
			&e.SpeakerID,
			&e.SpeakerName,
			&e.Text,
			&e.RawText,
			&e.NPCID,
			&e.Timestamp,
			&durationNS,
		); err != nil {
			return memory.TranscriptEntry{}, err
		}
		e.Duration = time.Duration(durationNS)
		return e, nil
	})
	if err != nil {
		return nil, fmt.Errorf("session store: scan rows: %w", err)
	}
	if entries == nil {
		entries = []memory.TranscriptEntry{}
	}
	return entries, nil
}
