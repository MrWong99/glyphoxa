package discord

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestPermissionChecker_IsDM(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		dmRoleID string
		inter    *discordgo.InteractionCreate
		want     bool
	}{
		{
			name:     "user with DM role",
			dmRoleID: "role-123",
			inter: &discordgo.InteractionCreate{
				Interaction: &discordgo.Interaction{
					Member: &discordgo.Member{
						Roles: []string{"role-456", "role-123", "role-789"},
					},
				},
			},
			want: true,
		},
		{
			name:     "user without DM role",
			dmRoleID: "role-123",
			inter: &discordgo.InteractionCreate{
				Interaction: &discordgo.Interaction{
					Member: &discordgo.Member{
						Roles: []string{"role-456", "role-789"},
					},
				},
			},
			want: false,
		},
		{
			name:     "empty DMRoleID allows all",
			dmRoleID: "",
			inter: &discordgo.InteractionCreate{
				Interaction: &discordgo.Interaction{
					Member: &discordgo.Member{
						Roles: []string{"role-456"},
					},
				},
			},
			want: true,
		},
		{
			name:     "nil Member returns false",
			dmRoleID: "role-123",
			inter: &discordgo.InteractionCreate{
				Interaction: &discordgo.Interaction{
					Member: nil,
				},
			},
			want: false,
		},
		{
			name:     "user with empty roles",
			dmRoleID: "role-123",
			inter: &discordgo.InteractionCreate{
				Interaction: &discordgo.Interaction{
					Member: &discordgo.Member{
						Roles: []string{},
					},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pc := NewPermissionChecker(tt.dmRoleID)
			got := pc.IsDM(tt.inter)
			if got != tt.want {
				t.Errorf("IsDM() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewCommandRouter(t *testing.T) {
	t.Parallel()

	r := NewCommandRouter()
	if r == nil {
		t.Fatal("NewCommandRouter() returned nil")
	}
	if len(r.commands) != 0 {
		t.Errorf("expected empty commands map, got %d entries", len(r.commands))
	}
	if len(r.autocomplete) != 0 {
		t.Errorf("expected empty autocomplete map, got %d entries", len(r.autocomplete))
	}
	if len(r.components) != 0 {
		t.Errorf("expected empty components map, got %d entries", len(r.components))
	}
	if len(r.modals) != 0 {
		t.Errorf("expected empty modals map, got %d entries", len(r.modals))
	}
}

func TestCommandRouter_ApplicationCommands(t *testing.T) {
	t.Parallel()

	r := NewCommandRouter()

	cmd := &discordgo.ApplicationCommand{Name: "test"}
	r.RegisterCommand("test", cmd, func(s *discordgo.Session, i *discordgo.InteractionCreate) {})

	cmds := r.ApplicationCommands()
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command, got %d", len(cmds))
	}
	if cmds[0].Name != "test" {
		t.Errorf("expected command name 'test', got %q", cmds[0].Name)
	}
}

func TestCommandRouter_ApplicationCommands_Dedup(t *testing.T) {
	t.Parallel()

	r := NewCommandRouter()

	cmd := &discordgo.ApplicationCommand{Name: "npc"}
	r.RegisterCommand("npc/mute", cmd, func(s *discordgo.Session, i *discordgo.InteractionCreate) {})
	r.RegisterCommand("npc/unmute", cmd, func(s *discordgo.Session, i *discordgo.InteractionCreate) {})

	cmds := r.ApplicationCommands()
	if len(cmds) != 1 {
		t.Fatalf("expected 1 deduplicated command, got %d", len(cmds))
	}
}

func TestCommandRouter_RegisterHandler(t *testing.T) {
	t.Parallel()

	r := NewCommandRouter()
	called := false
	r.RegisterHandler("test", func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		called = true
	})

	// Handler without command definition should not appear in ApplicationCommands.
	cmds := r.ApplicationCommands()
	if len(cmds) != 0 {
		t.Errorf("expected 0 commands, got %d", len(cmds))
	}

	// But the handler should still be accessible.
	entry, ok := r.commands["test"]
	if !ok {
		t.Fatal("expected handler to be registered")
	}
	entry.handler(nil, nil)
	if !called {
		t.Error("handler was not called")
	}
}
