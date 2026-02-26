package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgxvec "github.com/pgvector/pgvector-go/pgx"

	"github.com/MrWong99/glyphoxa/pkg/memory"
)

// Compile-time interface checks.
//
// L1 (SessionStore) and L2 (SemanticIndex) both define a method named Search
// but with different signatures. Go does not allow a single struct to implement
// both simultaneously, so they are exposed as sub-types via [Store.L1] and
// [Store.L2].
//
// L3 KnowledgeGraph and GraphRAGQuerier have no conflicting method names and
// are implemented directly on *Store.
var (
	_ memory.SessionStore    = (*SessionStoreImpl)(nil)
	_ memory.SemanticIndex   = (*SemanticIndexImpl)(nil)
	_ memory.KnowledgeGraph  = (*Store)(nil)
	_ memory.GraphRAGQuerier = (*Store)(nil)
)

// Store is the central PostgreSQL-backed memory store for Glyphoxa. It holds a
// single [pgxpool.Pool] and exposes the three-layer memory architecture:
//
//   - [Store.L1] returns a [SessionStoreImpl] implementing [memory.SessionStore]
//   - [Store.L2] returns a [SemanticIndexImpl] implementing [memory.SemanticIndex]
//   - Store itself implements [memory.KnowledgeGraph] and [memory.GraphRAGQuerier]
//
// All operations are safe for concurrent use.
type Store struct {
	pool     *pgxpool.Pool
	sessions *SessionStoreImpl
	semantic *SemanticIndexImpl
}

// NewStore creates a new Store, establishes a connection pool to the PostgreSQL
// database at dsn, registers pgvector types on every connection, and runs
// [Migrate] to ensure all required tables and extensions exist.
//
// embeddingDimensions must match the output dimension of the embedding model
// used to produce [memory.Chunk.Embedding] values (e.g., 1536 for OpenAI
// text-embedding-3-small). Changing this value after the first migration
// requires a manual schema change.
func NewStore(ctx context.Context, dsn string, embeddingDimensions int) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres store: parse dsn: %w", err)
	}

	// Register pgvector types on every new connection so that vector columns
	// can be scanned into and inserted from pgvector.Vector values.
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		return pgxvec.RegisterTypes(ctx, conn)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres store: create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres store: ping: %w", err)
	}

	if err := Migrate(ctx, pool, embeddingDimensions); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres store: migrate: %w", err)
	}

	return &Store{
		pool:     pool,
		sessions: &SessionStoreImpl{pool: pool},
		semantic: &SemanticIndexImpl{pool: pool},
	}, nil
}

// L1 returns the L1 session log implementation which satisfies [memory.SessionStore].
func (s *Store) L1() *SessionStoreImpl { return s.sessions }

// L2 returns the L2 semantic index implementation which satisfies [memory.SemanticIndex].
func (s *Store) L2() *SemanticIndexImpl { return s.semantic }

// Close releases all connections held by the underlying connection pool.
// It should be called when the Store is no longer needed, typically via defer.
func (s *Store) Close() {
	s.pool.Close()
}
