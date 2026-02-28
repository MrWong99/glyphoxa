package commands

import (
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/MrWong99/glyphoxa/internal/agent"
	"github.com/MrWong99/glyphoxa/internal/agent/mock"
	"github.com/MrWong99/glyphoxa/internal/agent/orchestrator"
	"github.com/MrWong99/glyphoxa/internal/discord"
)

func newTestOrchestrator(agents ...agent.NPCAgent) *orchestrator.Orchestrator {
	return orchestrator.New(agents)
}

func TestNPCDefinition(t *testing.T) {
	t.Parallel()

	nc := NewNPCCommands(discord.NewPermissionChecker(""), nil)
	def := nc.Definition()

	if def.Name != "npc" {
		t.Errorf("Name = %q, want %q", def.Name, "npc")
	}

	expectedSubs := []string{"list", "mute", "unmute", "speak", "muteall", "unmuteall"}
	if len(def.Options) != len(expectedSubs) {
		t.Fatalf("Options count = %d, want %d", len(def.Options), len(expectedSubs))
	}

	for i, name := range expectedSubs {
		if def.Options[i].Name != name {
			t.Errorf("subcommand[%d] = %q, want %q", i, def.Options[i].Name, name)
		}
		if def.Options[i].Type != discordgo.ApplicationCommandOptionSubCommand {
			t.Errorf("subcommand[%d] type = %d, want SubCommand", i, def.Options[i].Type)
		}
	}

	// Verify mute has a "name" option with autocomplete.
	muteOpts := def.Options[1].Options
	if len(muteOpts) != 1 {
		t.Fatalf("mute options count = %d, want 1", len(muteOpts))
	}
	if muteOpts[0].Name != "name" {
		t.Errorf("mute option name = %q, want %q", muteOpts[0].Name, "name")
	}
	if !muteOpts[0].Autocomplete {
		t.Error("mute name option should have autocomplete enabled")
	}

	// Verify speak has "name" and "text" options.
	speakOpts := def.Options[3].Options
	if len(speakOpts) != 2 {
		t.Fatalf("speak options count = %d, want 2", len(speakOpts))
	}
	if speakOpts[0].Name != "name" {
		t.Errorf("speak option[0] = %q, want %q", speakOpts[0].Name, "name")
	}
	if speakOpts[1].Name != "text" {
		t.Errorf("speak option[1] = %q, want %q", speakOpts[1].Name, "text")
	}
}

func TestNPCList_NoSession(t *testing.T) {
	t.Parallel()

	perms := discord.NewPermissionChecker("")
	nc := NewNPCCommands(perms, func() *orchestrator.Orchestrator { return nil })

	// Verify the handler does not panic when no orchestrator is available.
	// We cannot easily test the Discord response without a real session,
	// so we verify the orchestrator check by confirming getOrch returns nil.
	orch := nc.getOrch()
	if orch != nil {
		t.Fatal("expected nil orchestrator")
	}
}

func TestNPCList_WithAgents(t *testing.T) {
	t.Parallel()

	npc1 := &mock.NPCAgent{
		IDResult:   "greymantle",
		NameResult: "Greymantle the Sage",
		IdentityResult: agent.NPCIdentity{
			Name:        "Greymantle the Sage",
			Personality: "A wise old sage.",
		},
	}
	npc2 := &mock.NPCAgent{
		IDResult:   "thorn",
		NameResult: "Thorn",
		IdentityResult: agent.NPCIdentity{
			Name:        "Thorn",
			Personality: "A gruff blacksmith.",
		},
	}

	orch := newTestOrchestrator(npc1, npc2)

	perms := discord.NewPermissionChecker("")
	nc := NewNPCCommands(perms, func() *orchestrator.Orchestrator { return orch })

	// Verify orchestrator has agents.
	agents := nc.getOrch().ActiveAgents()
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}

	// Verify mute state queries work.
	for _, a := range agents {
		muted, err := orch.IsMuted(a.ID())
		if err != nil {
			t.Fatalf("IsMuted(%q) error: %v", a.ID(), err)
		}
		if muted {
			t.Errorf("agent %q should not be muted by default", a.ID())
		}
	}
}

func TestNPCMuteUnmute(t *testing.T) {
	t.Parallel()

	npc := &mock.NPCAgent{
		IDResult:   "greymantle",
		NameResult: "Greymantle",
	}
	orch := newTestOrchestrator(npc)

	// Mute the agent.
	if err := orch.MuteAgent("greymantle"); err != nil {
		t.Fatalf("MuteAgent error: %v", err)
	}
	muted, err := orch.IsMuted("greymantle")
	if err != nil {
		t.Fatalf("IsMuted error: %v", err)
	}
	if !muted {
		t.Error("expected agent to be muted")
	}

	// Unmute.
	if err := orch.UnmuteAgent("greymantle"); err != nil {
		t.Fatalf("UnmuteAgent error: %v", err)
	}
	muted, err = orch.IsMuted("greymantle")
	if err != nil {
		t.Fatalf("IsMuted error: %v", err)
	}
	if muted {
		t.Error("expected agent to be unmuted")
	}
}

func TestNPCMuteAll_UnmuteAll(t *testing.T) {
	t.Parallel()

	npc1 := &mock.NPCAgent{IDResult: "a", NameResult: "Alpha"}
	npc2 := &mock.NPCAgent{IDResult: "b", NameResult: "Beta"}
	orch := newTestOrchestrator(npc1, npc2)

	count := orch.MuteAll()
	if count != 2 {
		t.Errorf("MuteAll = %d, want 2", count)
	}

	// Muting again should change 0.
	count = orch.MuteAll()
	if count != 0 {
		t.Errorf("second MuteAll = %d, want 0", count)
	}

	count = orch.UnmuteAll()
	if count != 2 {
		t.Errorf("UnmuteAll = %d, want 2", count)
	}
}

func TestNPCAgentByName(t *testing.T) {
	t.Parallel()

	npc := &mock.NPCAgent{IDResult: "grey", NameResult: "Greymantle"}
	orch := newTestOrchestrator(npc)

	found := orch.AgentByName("greymantle")
	if found == nil {
		t.Fatal("expected to find agent by case-insensitive name")
	}
	if found.ID() != "grey" {
		t.Errorf("ID = %q, want %q", found.ID(), "grey")
	}

	notFound := orch.AgentByName("nonexistent")
	if notFound != nil {
		t.Error("expected nil for nonexistent agent name")
	}
}

func TestSubcommandStringOption(t *testing.T) {
	t.Parallel()

	i := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type: discordgo.InteractionApplicationCommand,
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "npc",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{
						Name: "mute",
						Type: discordgo.ApplicationCommandOptionSubCommand,
						Options: []*discordgo.ApplicationCommandInteractionDataOption{
							{
								Name:  "name",
								Type:  discordgo.ApplicationCommandOptionString,
								Value: "Greymantle",
							},
						},
					},
				},
			},
		},
	}

	got := subcommandStringOption(i, "name")
	if got != "Greymantle" {
		t.Errorf("subcommandStringOption = %q, want %q", got, "Greymantle")
	}

	got = subcommandStringOption(i, "nonexistent")
	if got != "" {
		t.Errorf("subcommandStringOption for missing = %q, want empty", got)
	}
}

func TestNPCRegister(t *testing.T) {
	t.Parallel()

	perms := discord.NewPermissionChecker("")
	nc := NewNPCCommands(perms, func() *orchestrator.Orchestrator { return nil })
	router := discord.NewCommandRouter()

	nc.Register(router)

	// Verify command definitions are registered.
	cmds := router.ApplicationCommands()
	if len(cmds) != 1 {
		t.Fatalf("expected 1 application command, got %d", len(cmds))
	}
	if cmds[0].Name != "npc" {
		t.Errorf("command name = %q, want %q", cmds[0].Name, "npc")
	}
}
