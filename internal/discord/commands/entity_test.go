package commands

import (
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/MrWong99/glyphoxa/internal/discord"
	"github.com/MrWong99/glyphoxa/internal/entity"
)

func newTestEntityCommands(store entity.Store, dmRoleID string) *EntityCommands {
	return NewEntityCommands(
		discord.NewPermissionChecker(dmRoleID),
		func() entity.Store { return store },
	)
}

func TestEntityDefinition(t *testing.T) {
	t.Parallel()

	ec := newTestEntityCommands(entity.NewMemStore(), "")
	def := ec.Definition()

	if def.Name != "entity" {
		t.Errorf("Name = %q, want %q", def.Name, "entity")
	}

	wantSubs := []string{"add", "list", "remove", "import"}
	if len(def.Options) != len(wantSubs) {
		t.Fatalf("subcommand count = %d, want %d", len(def.Options), len(wantSubs))
	}
	for i, want := range wantSubs {
		if def.Options[i].Name != want {
			t.Errorf("subcommand[%d] = %q, want %q", i, def.Options[i].Name, want)
		}
	}
}

func TestEntityDefinition_ListHasTypeFilter(t *testing.T) {
	t.Parallel()

	ec := newTestEntityCommands(entity.NewMemStore(), "")
	def := ec.Definition()

	// Find the list subcommand.
	var listOpt *discordgo.ApplicationCommandOption
	for _, opt := range def.Options {
		if opt.Name == "list" {
			listOpt = opt
			break
		}
	}
	if listOpt == nil {
		t.Fatal("list subcommand not found")
	}
	if len(listOpt.Options) == 0 {
		t.Fatal("list subcommand has no options")
	}
	typeOpt := listOpt.Options[0]
	if typeOpt.Name != "type" {
		t.Errorf("first option = %q, want %q", typeOpt.Name, "type")
	}
	if len(typeOpt.Choices) != 6 {
		t.Errorf("type choices = %d, want 6", len(typeOpt.Choices))
	}
}

func TestEntityDefinition_RemoveHasAutocomplete(t *testing.T) {
	t.Parallel()

	ec := newTestEntityCommands(entity.NewMemStore(), "")
	def := ec.Definition()

	var removeOpt *discordgo.ApplicationCommandOption
	for _, opt := range def.Options {
		if opt.Name == "remove" {
			removeOpt = opt
			break
		}
	}
	if removeOpt == nil {
		t.Fatal("remove subcommand not found")
	}
	if len(removeOpt.Options) == 0 {
		t.Fatal("remove subcommand has no options")
	}
	nameOpt := removeOpt.Options[0]
	if nameOpt.Name != "name" {
		t.Errorf("option name = %q, want %q", nameOpt.Name, "name")
	}
	if !nameOpt.Autocomplete {
		t.Error("name option should have Autocomplete = true")
	}
}

func TestEntityRegister(t *testing.T) {
	t.Parallel()

	store := entity.NewMemStore()
	ec := newTestEntityCommands(store, "")
	router := discord.NewCommandRouter()
	ec.Register(router)

	// The router should have the entity command registered.
	cmds := router.ApplicationCommands()
	found := false
	for _, cmd := range cmds {
		if cmd.Name == "entity" {
			found = true
			break
		}
	}
	if !found {
		t.Error("entity command not registered with router")
	}
}

func TestEntityAdd_NoDMRole(t *testing.T) {
	t.Parallel()

	perms := discord.NewPermissionChecker("dm-role-123")
	ec := NewEntityCommands(perms, func() entity.Store { return entity.NewMemStore() })

	i := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Member: &discordgo.Member{
				User:  &discordgo.User{ID: "user-1"},
				Roles: []string{"other-role"},
			},
		},
	}

	if perms.IsDM(i) {
		t.Fatal("expected IsDM to return false for user without DM role")
	}

	// Verify the entity commands struct was constructed properly.
	if ec.perms != perms {
		t.Error("perms not set correctly")
	}
}

func TestSubcommandOptions(t *testing.T) {
	t.Parallel()

	t.Run("with subcommand options", func(t *testing.T) {
		t.Parallel()
		i := &discordgo.InteractionCreate{
			Interaction: &discordgo.Interaction{
				Type: discordgo.InteractionApplicationCommand,
				Data: discordgo.ApplicationCommandInteractionData{
					Name: "entity",
					Options: []*discordgo.ApplicationCommandInteractionDataOption{
						{
							Name: "list",
							Type: discordgo.ApplicationCommandOptionSubCommand,
							Options: []*discordgo.ApplicationCommandInteractionDataOption{
								{
									Name:  "type",
									Type:  discordgo.ApplicationCommandOptionString,
									Value: "npc",
								},
							},
						},
					},
				},
			},
		}

		opts := subcommandOptions(i)
		if len(opts) != 1 {
			t.Fatalf("got %d options, want 1", len(opts))
		}
		if opts[0].Name != "type" {
			t.Errorf("option name = %q, want %q", opts[0].Name, "type")
		}
	})

	t.Run("no subcommand", func(t *testing.T) {
		t.Parallel()
		i := &discordgo.InteractionCreate{
			Interaction: &discordgo.Interaction{
				Type: discordgo.InteractionApplicationCommand,
				Data: discordgo.ApplicationCommandInteractionData{
					Name: "entity",
				},
			},
		}

		opts := subcommandOptions(i)
		if opts != nil {
			t.Errorf("expected nil, got %v", opts)
		}
	})
}

func TestEntityModalFields(t *testing.T) {
	t.Parallel()

	ec := newTestEntityCommands(entity.NewMemStore(), "")
	def := ec.Definition()

	// Verify the add subcommand exists (modal is opened from it).
	var addOpt *discordgo.ApplicationCommandOption
	for _, opt := range def.Options {
		if opt.Name == "add" {
			addOpt = opt
			break
		}
	}
	if addOpt == nil {
		t.Fatal("add subcommand not found")
	}
	// The add subcommand has no inline options — it opens a modal.
	if len(addOpt.Options) != 0 {
		t.Errorf("add subcommand options = %d, want 0 (modal-based)", len(addOpt.Options))
	}
}
