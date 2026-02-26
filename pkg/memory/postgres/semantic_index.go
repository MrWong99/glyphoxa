package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgvector "github.com/pgvector/pgvector-go"

	"github.com/MrWong99/glyphoxa/pkg/memory"
)

// SemanticIndexImpl is the L2 memory layer backed by a PostgreSQL chunks table
// with a pgvector HNSW index for fast approximate nearest-neighbour search.
//
// Obtain one via [Store.L2] rather than constructing directly.
// All methods are safe for concurrent use.
type SemanticIndexImpl struct {
	pool *pgxpool.Pool
}

// IndexChunk implements [memory.SemanticIndex]. It upserts a pre-embedded
// [memory.Chunk] into the chunks table. If a chunk with the same ID already
// exists it is completely replaced.
func (s *SemanticIndexImpl) IndexChunk(ctx context.Context, chunk memory.Chunk) error {
	const q = `
		INSERT INTO chunks
		    (id, session_id, content, embedding, speaker_id, entity_id, topic, timestamp)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO UPDATE SET
		    session_id  = EXCLUDED.session_id,
		    content     = EXCLUDED.content,
		    embedding   = EXCLUDED.embedding,
		    speaker_id  = EXCLUDED.speaker_id,
		    entity_id   = EXCLUDED.entity_id,
		    topic       = EXCLUDED.topic,
		    timestamp   = EXCLUDED.timestamp`

	vec := pgvector.NewVector(chunk.Embedding)
	_, err := s.pool.Exec(ctx, q,
		chunk.ID,
		chunk.SessionID,
		chunk.Content,
		vec,
		chunk.SpeakerID,
		chunk.EntityID,
		chunk.Topic,
		chunk.Timestamp,
	)
	if err != nil {
		return fmt.Errorf("semantic index: index chunk: %w", err)
	}
	return nil
}

// Search implements [memory.SemanticIndex]. It finds the topK chunks whose
// embeddings are closest (cosine distance) to the supplied query embedding,
// optionally filtered by filter.
//
// Results are ordered by ascending cosine distance (most similar first).
func (s *SemanticIndexImpl) Search(ctx context.Context, embedding []float32, topK int, filter memory.ChunkFilter) ([]memory.ChunkResult, error) {
	queryVec := pgvector.NewVector(embedding)

	args := []any{queryVec} // $1 = query vector
	next := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	var conditions []string
	if filter.SessionID != "" {
		conditions = append(conditions, "session_id = "+next(filter.SessionID))
	}
	if filter.SpeakerID != "" {
		conditions = append(conditions, "speaker_id = "+next(filter.SpeakerID))
	}
	if filter.EntityID != "" {
		conditions = append(conditions, "entity_id = "+next(filter.EntityID))
	}
	if !filter.After.IsZero() {
		conditions = append(conditions, "timestamp > "+next(filter.After))
	}
	if !filter.Before.IsZero() {
		conditions = append(conditions, "timestamp < "+next(filter.Before))
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, "\n  AND ")
	}

	args = append(args, topK)
	limitArg := fmt.Sprintf("$%d", len(args))

	q := fmt.Sprintf(`
		SELECT id, session_id, content, embedding, speaker_id, entity_id, topic, timestamp,
		       embedding <=> $1 AS distance
		FROM   chunks
		%s
		ORDER  BY distance
		LIMIT  %s`, whereClause, limitArg)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("semantic index: search: %w", err)
	}

	results, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (memory.ChunkResult, error) {
		var (
			cr  memory.ChunkResult
			vec pgvector.Vector
		)
		if err := row.Scan(
			&cr.Chunk.ID,
			&cr.Chunk.SessionID,
			&cr.Chunk.Content,
			&vec,
			&cr.Chunk.SpeakerID,
			&cr.Chunk.EntityID,
			&cr.Chunk.Topic,
			&cr.Chunk.Timestamp,
			&cr.Distance,
		); err != nil {
			return memory.ChunkResult{}, err
		}
		cr.Chunk.Embedding = vec.Slice()
		return cr, nil
	})
	if err != nil {
		return nil, fmt.Errorf("semantic index: scan rows: %w", err)
	}
	if results == nil {
		results = []memory.ChunkResult{}
	}
	return results, nil
}
