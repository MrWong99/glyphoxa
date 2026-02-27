package npcstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ---------------------------------------------------------------------------
// Test helpers — mock DB types
// ---------------------------------------------------------------------------

// mockRow implements pgx.Row for testing.
type mockRow struct {
	scanFunc func(dest ...any) error
}

func (r *mockRow) Scan(dest ...any) error { return r.scanFunc(dest...) }

// mockRows implements pgx.Rows for testing.
type mockRows struct {
	data    [][]any
	idx     int
	err     error
	closed  bool
	scanErr error
}

func (r *mockRows) Close()                                       { r.closed = true }
func (r *mockRows) Err() error                                   { return r.err }
func (r *mockRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *mockRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *mockRows) RawValues() [][]byte                          { return nil }
func (r *mockRows) Conn() *pgx.Conn                              { return nil }

func (r *mockRows) Next() bool {
	if r.idx >= len(r.data) {
		return false
	}
	r.idx++
	return true
}

func (r *mockRows) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	row := r.data[r.idx-1]
	if len(dest) != len(row) {
		return fmt.Errorf("scan: expected %d columns, got %d destinations", len(row), len(dest))
	}
	for i, v := range row {
		switch d := dest[i].(type) {
		case *string:
			*d = v.(string)
		case *[]byte:
			*d = v.([]byte)
		case *time.Time:
			*d = v.(time.Time)
		default:
			return fmt.Errorf("scan: unsupported type at index %d: %T", i, dest[i])
		}
	}
	return nil
}

func (r *mockRows) Values() ([]any, error) { return nil, nil }

// mockDB implements the DB interface for testing.
type mockDB struct {
	queryRowFunc func(ctx context.Context, sql string, args ...any) pgx.Row
	queryFunc    func(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	execFunc     func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func (m *mockDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if m.queryRowFunc != nil {
		return m.queryRowFunc(ctx, sql, args...)
	}
	return &mockRow{scanFunc: func(dest ...any) error { return pgx.ErrNoRows }}
}

func (m *mockDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if m.queryFunc != nil {
		return m.queryFunc(ctx, sql, args...)
	}
	return &mockRows{}, nil
}

func (m *mockDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if m.execFunc != nil {
		return m.execFunc(ctx, sql, args...)
	}
	return pgconn.CommandTag{}, nil
}

// ---------------------------------------------------------------------------
// Validate tests
// ---------------------------------------------------------------------------

func TestNPCDefinition_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		def     NPCDefinition
		wantErr []string // substrings that must appear in the error
	}{
		{
			name: "valid minimal",
			def: NPCDefinition{
				Name: "Test NPC",
			},
		},
		{
			name: "valid full",
			def: NPCDefinition{
				Name:       "Greymantle",
				Engine:     "s2s",
				BudgetTier: "deep",
				Voice: VoiceConfig{
					PitchShift:  5,
					SpeedFactor: 1.5,
				},
			},
		},
		{
			name: "valid boundary speed 0.5",
			def: NPCDefinition{
				Name:  "NPC",
				Voice: VoiceConfig{SpeedFactor: 0.5},
			},
		},
		{
			name: "valid boundary speed 2.0",
			def: NPCDefinition{
				Name:  "NPC",
				Voice: VoiceConfig{SpeedFactor: 2.0},
			},
		},
		{
			name: "valid boundary pitch -10",
			def: NPCDefinition{
				Name:  "NPC",
				Voice: VoiceConfig{PitchShift: -10},
			},
		},
		{
			name: "valid boundary pitch 10",
			def: NPCDefinition{
				Name:  "NPC",
				Voice: VoiceConfig{PitchShift: 10},
			},
		},
		{
			name: "valid zero speed factor uses default",
			def: NPCDefinition{
				Name:  "NPC",
				Voice: VoiceConfig{SpeedFactor: 0},
			},
		},
		{
			name:    "empty name",
			def:     NPCDefinition{},
			wantErr: []string{"name must not be empty"},
		},
		{
			name: "invalid engine",
			def: NPCDefinition{
				Name:   "NPC",
				Engine: "turbo",
			},
			wantErr: []string{`engine must be "cascaded", "s2s", or "sentence_cascade"`},
		},
		{
			name: "invalid budget tier",
			def: NPCDefinition{
				Name:       "NPC",
				BudgetTier: "unlimited",
			},
			wantErr: []string{`budget_tier must be "fast", "standard", or "deep"`},
		},
		{
			name: "speed factor too low",
			def: NPCDefinition{
				Name:  "NPC",
				Voice: VoiceConfig{SpeedFactor: 0.1},
			},
			wantErr: []string{"voice speed_factor must be in [0.5, 2.0]"},
		},
		{
			name: "speed factor too high",
			def: NPCDefinition{
				Name:  "NPC",
				Voice: VoiceConfig{SpeedFactor: 3.0},
			},
			wantErr: []string{"voice speed_factor must be in [0.5, 2.0]"},
		},
		{
			name: "pitch shift too low",
			def: NPCDefinition{
				Name:  "NPC",
				Voice: VoiceConfig{PitchShift: -11},
			},
			wantErr: []string{"voice pitch_shift must be in [-10, 10]"},
		},
		{
			name: "pitch shift too high",
			def: NPCDefinition{
				Name:  "NPC",
				Voice: VoiceConfig{PitchShift: 10.5},
			},
			wantErr: []string{"voice pitch_shift must be in [-10, 10]"},
		},
		{
			name: "multiple errors",
			def: NPCDefinition{
				Engine:     "warp",
				BudgetTier: "ultra",
				Voice:      VoiceConfig{SpeedFactor: 0.1, PitchShift: -20},
			},
			wantErr: []string{
				"name must not be empty",
				"engine must be",
				"budget_tier must be",
				"speed_factor",
				"pitch_shift",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.def.Validate()

			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("Validate() unexpected error: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatal("Validate() expected error, got nil")
			}

			errStr := err.Error()
			for _, want := range tt.wantErr {
				if !strings.Contains(errStr, want) {
					t.Errorf("Validate() error = %q, want substring %q", errStr, want)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ToIdentity tests
// ---------------------------------------------------------------------------

func TestToIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		def  NPCDefinition
	}{
		{
			name: "full conversion",
			def: NPCDefinition{
				Name:        "Greymantle",
				Personality: "Wise and ancient sage",
				Voice: VoiceConfig{
					Provider:    "elevenlabs",
					VoiceID:     "abc123",
					PitchShift:  2.5,
					SpeedFactor: 1.2,
				},
				KnowledgeScope:  []string{"history", "magic"},
				SecretKnowledge: []string{"the sword is cursed"},
				BehaviorRules:   []string{"speak in archaic English"},
			},
		},
		{
			name: "minimal conversion",
			def: NPCDefinition{
				Name: "Simple NPC",
			},
		},
		{
			name: "nil slices",
			def: NPCDefinition{
				Name:            "Nil NPC",
				KnowledgeScope:  nil,
				SecretKnowledge: nil,
				BehaviorRules:   nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			identity := ToIdentity(&tt.def)

			if identity.Name != tt.def.Name {
				t.Errorf("Name = %q, want %q", identity.Name, tt.def.Name)
			}
			if identity.Personality != tt.def.Personality {
				t.Errorf("Personality = %q, want %q", identity.Personality, tt.def.Personality)
			}
			if identity.Voice.ID != tt.def.Voice.VoiceID {
				t.Errorf("Voice.ID = %q, want %q", identity.Voice.ID, tt.def.Voice.VoiceID)
			}
			if identity.Voice.Name != tt.def.Name {
				t.Errorf("Voice.Name = %q, want %q", identity.Voice.Name, tt.def.Name)
			}
			if identity.Voice.Provider != tt.def.Voice.Provider {
				t.Errorf("Voice.Provider = %q, want %q", identity.Voice.Provider, tt.def.Voice.Provider)
			}
			if identity.Voice.PitchShift != tt.def.Voice.PitchShift {
				t.Errorf("Voice.PitchShift = %g, want %g", identity.Voice.PitchShift, tt.def.Voice.PitchShift)
			}
			if identity.Voice.SpeedFactor != tt.def.Voice.SpeedFactor {
				t.Errorf("Voice.SpeedFactor = %g, want %g", identity.Voice.SpeedFactor, tt.def.Voice.SpeedFactor)
			}
			assertStringSliceEqual(t, "KnowledgeScope", identity.KnowledgeScope, tt.def.KnowledgeScope)
			assertStringSliceEqual(t, "SecretKnowledge", identity.SecretKnowledge, tt.def.SecretKnowledge)
			assertStringSliceEqual(t, "BehaviorRules", identity.BehaviorRules, tt.def.BehaviorRules)
		})
	}
}

// ---------------------------------------------------------------------------
// PostgresStore tests
// ---------------------------------------------------------------------------

func TestPostgresStore_Migrate(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		db := &mockDB{
			execFunc: func(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
				if !strings.Contains(sql, "CREATE TABLE") {
					t.Errorf("Migrate SQL should contain CREATE TABLE, got: %s", sql)
				}
				return pgconn.CommandTag{}, nil
			},
		}
		store := NewPostgresStore(db)
		if err := store.Migrate(context.Background()); err != nil {
			t.Fatalf("Migrate() unexpected error: %v", err)
		}
	})

	t.Run("error", func(t *testing.T) {
		t.Parallel()
		db := &mockDB{
			execFunc: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
				return pgconn.CommandTag{}, errors.New("connection refused")
			},
		}
		store := NewPostgresStore(db)
		err := store.Migrate(context.Background())
		if err == nil {
			t.Fatal("Migrate() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "npcstore: migrate:") {
			t.Errorf("error = %q, want prefix 'npcstore: migrate:'", err.Error())
		}
	})
}

func TestPostgresStore_Create(t *testing.T) {
	t.Parallel()

	fixedTime := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		var capturedSQL string
		var capturedArgs []any

		db := &mockDB{
			queryRowFunc: func(_ context.Context, sql string, args ...any) pgx.Row {
				capturedSQL = sql
				capturedArgs = args
				return &mockRow{
					scanFunc: func(dest ...any) error {
						*(dest[0].(*time.Time)) = fixedTime
						*(dest[1].(*time.Time)) = fixedTime
						return nil
					},
				}
			},
		}

		store := NewPostgresStore(db)
		def := &NPCDefinition{
			ID:         "npc-1",
			CampaignID: "camp-1",
			Name:       "Greymantle",
		}

		err := store.Create(context.Background(), def)
		if err != nil {
			t.Fatalf("Create() unexpected error: %v", err)
		}

		if !strings.Contains(capturedSQL, "INSERT INTO npc_definitions") {
			t.Errorf("SQL should contain INSERT, got: %s", capturedSQL)
		}
		if len(capturedArgs) != 12 {
			t.Errorf("expected 12 args, got %d", len(capturedArgs))
		}
		if capturedArgs[0] != "npc-1" {
			t.Errorf("first arg = %v, want 'npc-1'", capturedArgs[0])
		}
		if def.CreatedAt != fixedTime {
			t.Errorf("CreatedAt = %v, want %v", def.CreatedAt, fixedTime)
		}
		if def.UpdatedAt != fixedTime {
			t.Errorf("UpdatedAt = %v, want %v", def.UpdatedAt, fixedTime)
		}
	})

	t.Run("validation error", func(t *testing.T) {
		t.Parallel()
		store := NewPostgresStore(&mockDB{})
		err := store.Create(context.Background(), &NPCDefinition{})
		if err == nil {
			t.Fatal("Create() expected validation error, got nil")
		}
		if !strings.Contains(err.Error(), "name must not be empty") {
			t.Errorf("error = %q, want validation error", err.Error())
		}
	})

	t.Run("duplicate key", func(t *testing.T) {
		t.Parallel()
		db := &mockDB{
			queryRowFunc: func(_ context.Context, _ string, _ ...any) pgx.Row {
				return &mockRow{
					scanFunc: func(_ ...any) error {
						return &pgconn.PgError{Code: "23505"}
					},
				}
			},
		}
		store := NewPostgresStore(db)
		err := store.Create(context.Background(), &NPCDefinition{ID: "dup", Name: "Dup"})
		if err == nil {
			t.Fatal("Create() expected duplicate error, got nil")
		}
		if !strings.Contains(err.Error(), "already exists") {
			t.Errorf("error = %q, want 'already exists'", err.Error())
		}
	})

	t.Run("db error", func(t *testing.T) {
		t.Parallel()
		db := &mockDB{
			queryRowFunc: func(_ context.Context, _ string, _ ...any) pgx.Row {
				return &mockRow{
					scanFunc: func(_ ...any) error {
						return errors.New("connection lost")
					},
				}
			},
		}
		store := NewPostgresStore(db)
		err := store.Create(context.Background(), &NPCDefinition{ID: "x", Name: "X"})
		if err == nil {
			t.Fatal("Create() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "npcstore: create:") {
			t.Errorf("error = %q, want prefix 'npcstore: create:'", err.Error())
		}
	})

	t.Run("defaults engine and budget tier", func(t *testing.T) {
		t.Parallel()

		var capturedArgs []any
		db := &mockDB{
			queryRowFunc: func(_ context.Context, _ string, args ...any) pgx.Row {
				capturedArgs = args
				return &mockRow{
					scanFunc: func(dest ...any) error {
						*(dest[0].(*time.Time)) = fixedTime
						*(dest[1].(*time.Time)) = fixedTime
						return nil
					},
				}
			},
		}

		store := NewPostgresStore(db)
		def := &NPCDefinition{ID: "npc-2", Name: "NPC"}
		if err := store.Create(context.Background(), def); err != nil {
			t.Fatalf("Create() unexpected error: %v", err)
		}

		// engine is arg index 4 (0-based), budget_tier is arg index 10
		if capturedArgs[4] != "cascaded" {
			t.Errorf("engine = %v, want 'cascaded'", capturedArgs[4])
		}
		if capturedArgs[10] != "fast" {
			t.Errorf("budget_tier = %v, want 'fast'", capturedArgs[10])
		}
	})
}

func TestPostgresStore_Get(t *testing.T) {
	t.Parallel()

	fixedTime := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

	t.Run("found", func(t *testing.T) {
		t.Parallel()

		db := &mockDB{
			queryRowFunc: func(_ context.Context, sql string, args ...any) pgx.Row {
				if args[0] != "npc-1" {
					t.Errorf("Get() id = %v, want 'npc-1'", args[0])
				}
				return &mockRow{
					scanFunc: func(dest ...any) error {
						*(dest[0].(*string)) = "npc-1"
						*(dest[1].(*string)) = "camp-1"
						*(dest[2].(*string)) = "Greymantle"
						*(dest[3].(*string)) = "Wise"
						*(dest[4].(*string)) = "cascaded"
						*(dest[5].(*[]byte)) = []byte(`{"provider":"elevenlabs","voice_id":"v1","pitch_shift":0,"speed_factor":1.0}`)
						*(dest[6].(*[]byte)) = []byte(`["history"]`)
						*(dest[7].(*[]byte)) = []byte(`["secret"]`)
						*(dest[8].(*[]byte)) = []byte(`["rule1"]`)
						*(dest[9].(*[]byte)) = []byte(`["tool1"]`)
						*(dest[10].(*string)) = "fast"
						*(dest[11].(*[]byte)) = []byte(`{"race":"elf"}`)
						*(dest[12].(*time.Time)) = fixedTime
						*(dest[13].(*time.Time)) = fixedTime
						return nil
					},
				}
			},
		}

		store := NewPostgresStore(db)
		def, err := store.Get(context.Background(), "npc-1")
		if err != nil {
			t.Fatalf("Get() unexpected error: %v", err)
		}
		if def == nil {
			t.Fatal("Get() returned nil, want definition")
		}
		if def.ID != "npc-1" {
			t.Errorf("ID = %q, want 'npc-1'", def.ID)
		}
		if def.Name != "Greymantle" {
			t.Errorf("Name = %q, want 'Greymantle'", def.Name)
		}
		if def.Voice.Provider != "elevenlabs" {
			t.Errorf("Voice.Provider = %q, want 'elevenlabs'", def.Voice.Provider)
		}
		if len(def.KnowledgeScope) != 1 || def.KnowledgeScope[0] != "history" {
			t.Errorf("KnowledgeScope = %v, want [history]", def.KnowledgeScope)
		}
		if def.Attributes["race"] != "elf" {
			t.Errorf("Attributes[race] = %v, want 'elf'", def.Attributes["race"])
		}
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()
		db := &mockDB{
			queryRowFunc: func(_ context.Context, _ string, _ ...any) pgx.Row {
				return &mockRow{
					scanFunc: func(_ ...any) error { return pgx.ErrNoRows },
				}
			},
		}
		store := NewPostgresStore(db)
		def, err := store.Get(context.Background(), "missing")
		if err != nil {
			t.Fatalf("Get() unexpected error: %v", err)
		}
		if def != nil {
			t.Errorf("Get() = %v, want nil for missing NPC", def)
		}
	})

	t.Run("db error", func(t *testing.T) {
		t.Parallel()
		db := &mockDB{
			queryRowFunc: func(_ context.Context, _ string, _ ...any) pgx.Row {
				return &mockRow{
					scanFunc: func(_ ...any) error { return errors.New("timeout") },
				}
			},
		}
		store := NewPostgresStore(db)
		_, err := store.Get(context.Background(), "npc-1")
		if err == nil {
			t.Fatal("Get() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "npcstore: get") {
			t.Errorf("error = %q, want prefix 'npcstore: get'", err.Error())
		}
	})
}

func TestPostgresStore_Update(t *testing.T) {
	t.Parallel()

	fixedTime := time.Date(2026, 1, 15, 13, 0, 0, 0, time.UTC)

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		db := &mockDB{
			queryRowFunc: func(_ context.Context, sql string, args ...any) pgx.Row {
				if !strings.Contains(sql, "UPDATE npc_definitions") {
					t.Errorf("Update SQL should contain UPDATE, got: %s", sql)
				}
				return &mockRow{
					scanFunc: func(dest ...any) error {
						*(dest[0].(*time.Time)) = fixedTime
						return nil
					},
				}
			},
		}

		store := NewPostgresStore(db)
		def := &NPCDefinition{ID: "npc-1", Name: "Updated"}
		err := store.Update(context.Background(), def)
		if err != nil {
			t.Fatalf("Update() unexpected error: %v", err)
		}
		if def.UpdatedAt != fixedTime {
			t.Errorf("UpdatedAt = %v, want %v", def.UpdatedAt, fixedTime)
		}
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()
		db := &mockDB{
			queryRowFunc: func(_ context.Context, _ string, _ ...any) pgx.Row {
				return &mockRow{
					scanFunc: func(_ ...any) error { return pgx.ErrNoRows },
				}
			},
		}
		store := NewPostgresStore(db)
		err := store.Update(context.Background(), &NPCDefinition{ID: "missing", Name: "X"})
		if err == nil {
			t.Fatal("Update() expected error for missing NPC")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("error = %q, want 'not found'", err.Error())
		}
	})

	t.Run("validation error", func(t *testing.T) {
		t.Parallel()
		store := NewPostgresStore(&mockDB{})
		err := store.Update(context.Background(), &NPCDefinition{ID: "x"})
		if err == nil {
			t.Fatal("Update() expected validation error")
		}
		if !strings.Contains(err.Error(), "name must not be empty") {
			t.Errorf("error = %q, want validation error", err.Error())
		}
	})
}

func TestPostgresStore_Delete(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		var capturedSQL string
		var capturedArgs []any
		db := &mockDB{
			execFunc: func(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
				capturedSQL = sql
				capturedArgs = args
				return pgconn.CommandTag{}, nil
			},
		}

		store := NewPostgresStore(db)
		err := store.Delete(context.Background(), "npc-1")
		if err != nil {
			t.Fatalf("Delete() unexpected error: %v", err)
		}
		if !strings.Contains(capturedSQL, "DELETE FROM npc_definitions") {
			t.Errorf("SQL = %q, want DELETE statement", capturedSQL)
		}
		if len(capturedArgs) != 1 || capturedArgs[0] != "npc-1" {
			t.Errorf("args = %v, want [npc-1]", capturedArgs)
		}
	})

	t.Run("db error", func(t *testing.T) {
		t.Parallel()
		db := &mockDB{
			execFunc: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
				return pgconn.CommandTag{}, errors.New("disk full")
			},
		}
		store := NewPostgresStore(db)
		err := store.Delete(context.Background(), "npc-1")
		if err == nil {
			t.Fatal("Delete() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "npcstore: delete") {
			t.Errorf("error = %q, want prefix 'npcstore: delete'", err.Error())
		}
	})
}

func TestPostgresStore_List(t *testing.T) {
	t.Parallel()

	fixedTime := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

	makeRow := func(id, campaignID, name string) []any {
		return []any{
			id,           // id
			campaignID,   // campaign_id
			name,         // name
			"",           // personality
			"cascaded",   // engine
			[]byte(`{}`), // voice
			[]byte(`[]`), // knowledge_scope
			[]byte(`[]`), // secret_knowledge
			[]byte(`[]`), // behavior_rules
			[]byte(`[]`), // tools
			"fast",       // budget_tier
			[]byte(`{}`), // attributes
			fixedTime,    // created_at
			fixedTime,    // updated_at
		}
	}

	t.Run("all", func(t *testing.T) {
		t.Parallel()
		db := &mockDB{
			queryFunc: func(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
				if strings.Contains(sql, "WHERE campaign_id") {
					t.Error("List all should not filter by campaign_id")
				}
				if len(args) != 0 {
					t.Errorf("List all should have 0 args, got %d", len(args))
				}
				return &mockRows{
					data: [][]any{
						makeRow("npc-1", "camp-1", "Alpha"),
						makeRow("npc-2", "camp-2", "Beta"),
					},
				}, nil
			},
		}

		store := NewPostgresStore(db)
		defs, err := store.List(context.Background(), "")
		if err != nil {
			t.Fatalf("List() unexpected error: %v", err)
		}
		if len(defs) != 2 {
			t.Fatalf("List() returned %d defs, want 2", len(defs))
		}
		if defs[0].ID != "npc-1" {
			t.Errorf("defs[0].ID = %q, want 'npc-1'", defs[0].ID)
		}
		if defs[1].ID != "npc-2" {
			t.Errorf("defs[1].ID = %q, want 'npc-2'", defs[1].ID)
		}
	})

	t.Run("filtered by campaign", func(t *testing.T) {
		t.Parallel()
		db := &mockDB{
			queryFunc: func(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
				if !strings.Contains(sql, "WHERE campaign_id") {
					t.Error("List filtered should contain WHERE campaign_id")
				}
				if len(args) != 1 || args[0] != "camp-1" {
					t.Errorf("args = %v, want [camp-1]", args)
				}
				return &mockRows{
					data: [][]any{
						makeRow("npc-1", "camp-1", "Alpha"),
					},
				}, nil
			},
		}

		store := NewPostgresStore(db)
		defs, err := store.List(context.Background(), "camp-1")
		if err != nil {
			t.Fatalf("List() unexpected error: %v", err)
		}
		if len(defs) != 1 {
			t.Fatalf("List() returned %d defs, want 1", len(defs))
		}
	})

	t.Run("empty result", func(t *testing.T) {
		t.Parallel()
		db := &mockDB{
			queryFunc: func(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
				return &mockRows{}, nil
			},
		}

		store := NewPostgresStore(db)
		defs, err := store.List(context.Background(), "")
		if err != nil {
			t.Fatalf("List() unexpected error: %v", err)
		}
		if defs != nil {
			t.Errorf("List() = %v, want nil for empty result", defs)
		}
	})

	t.Run("query error", func(t *testing.T) {
		t.Parallel()
		db := &mockDB{
			queryFunc: func(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
				return nil, errors.New("connection reset")
			},
		}

		store := NewPostgresStore(db)
		_, err := store.List(context.Background(), "")
		if err == nil {
			t.Fatal("List() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "npcstore: list:") {
			t.Errorf("error = %q, want prefix 'npcstore: list:'", err.Error())
		}
	})

	t.Run("rows error after iteration", func(t *testing.T) {
		t.Parallel()
		db := &mockDB{
			queryFunc: func(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
				return &mockRows{err: errors.New("stream interrupted")}, nil
			},
		}

		store := NewPostgresStore(db)
		_, err := store.List(context.Background(), "")
		if err == nil {
			t.Fatal("List() expected error from rows.Err()")
		}
		if !strings.Contains(err.Error(), "npcstore: list:") {
			t.Errorf("error = %q, want prefix 'npcstore: list:'", err.Error())
		}
	})
}

func TestPostgresStore_Upsert(t *testing.T) {
	t.Parallel()

	fixedTime := time.Date(2026, 1, 15, 14, 0, 0, 0, time.UTC)

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		var capturedSQL string
		db := &mockDB{
			queryRowFunc: func(_ context.Context, sql string, _ ...any) pgx.Row {
				capturedSQL = sql
				return &mockRow{
					scanFunc: func(dest ...any) error {
						*(dest[0].(*time.Time)) = fixedTime
						*(dest[1].(*time.Time)) = fixedTime
						return nil
					},
				}
			},
		}

		store := NewPostgresStore(db)
		def := &NPCDefinition{ID: "npc-1", Name: "Upserted"}
		err := store.Upsert(context.Background(), def)
		if err != nil {
			t.Fatalf("Upsert() unexpected error: %v", err)
		}
		if !strings.Contains(capturedSQL, "ON CONFLICT") {
			t.Errorf("SQL should contain ON CONFLICT, got: %s", capturedSQL)
		}
		if def.CreatedAt != fixedTime {
			t.Errorf("CreatedAt = %v, want %v", def.CreatedAt, fixedTime)
		}
	})

	t.Run("validation error", func(t *testing.T) {
		t.Parallel()
		store := NewPostgresStore(&mockDB{})
		err := store.Upsert(context.Background(), &NPCDefinition{})
		if err == nil {
			t.Fatal("Upsert() expected validation error")
		}
	})

	t.Run("db error", func(t *testing.T) {
		t.Parallel()
		db := &mockDB{
			queryRowFunc: func(_ context.Context, _ string, _ ...any) pgx.Row {
				return &mockRow{
					scanFunc: func(_ ...any) error { return errors.New("deadlock") },
				}
			},
		}
		store := NewPostgresStore(db)
		err := store.Upsert(context.Background(), &NPCDefinition{ID: "x", Name: "X"})
		if err == nil {
			t.Fatal("Upsert() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "npcstore: upsert:") {
			t.Errorf("error = %q, want prefix 'npcstore: upsert:'", err.Error())
		}
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func TestEmptySlice(t *testing.T) {
	t.Parallel()

	t.Run("nil returns empty", func(t *testing.T) {
		t.Parallel()
		result := emptySlice(nil)
		if result == nil || len(result) != 0 {
			t.Errorf("emptySlice(nil) = %v, want []", result)
		}
	})

	t.Run("non-nil passes through", func(t *testing.T) {
		t.Parallel()
		input := []string{"a", "b"}
		result := emptySlice(input)
		if len(result) != 2 {
			t.Errorf("emptySlice(input) len = %d, want 2", len(result))
		}
	})
}

func TestEmptyMap(t *testing.T) {
	t.Parallel()

	t.Run("nil returns empty", func(t *testing.T) {
		t.Parallel()
		result := emptyMap(nil)
		if result == nil || len(result) != 0 {
			t.Errorf("emptyMap(nil) = %v, want {}", result)
		}
	})

	t.Run("non-nil passes through", func(t *testing.T) {
		t.Parallel()
		input := map[string]any{"k": "v"}
		result := emptyMap(input)
		if len(result) != 1 {
			t.Errorf("emptyMap(input) len = %d, want 1", len(result))
		}
	})
}

func TestDefaultEngine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"", "cascaded"},
		{"cascaded", "cascaded"},
		{"s2s", "s2s"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("input=%q", tt.input), func(t *testing.T) {
			t.Parallel()
			if got := defaultEngine(tt.input); got != tt.want {
				t.Errorf("defaultEngine(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDefaultBudgetTier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"", "fast"},
		{"fast", "fast"},
		{"standard", "standard"},
		{"deep", "deep"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("input=%q", tt.input), func(t *testing.T) {
			t.Parallel()
			if got := defaultBudgetTier(tt.input); got != tt.want {
				t.Errorf("defaultBudgetTier(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// assertStringSliceEqual compares two string slices for equality, treating nil
// and empty as equivalent.
func assertStringSliceEqual(t *testing.T, name string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: len = %d, want %d", name, len(got), len(want))
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("%s[%d] = %q, want %q", name, i, got[i], want[i])
		}
	}
}
