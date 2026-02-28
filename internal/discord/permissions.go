package discord

import (
	"slices"

	"github.com/bwmarrin/discordgo"
)

// PermissionChecker validates that a Discord user has the DM role
// before executing privileged slash commands.
type PermissionChecker struct {
	dmRoleID string
}

// NewPermissionChecker creates a PermissionChecker with the given DM role ID.
func NewPermissionChecker(dmRoleID string) *PermissionChecker {
	return &PermissionChecker{dmRoleID: dmRoleID}
}

// IsDM checks whether the interaction author has the configured DM role.
// If dmRoleID is empty, all users are treated as DMs (useful for development).
// Returns false if the interaction has no Member (e.g., DM channel interactions).
func (p *PermissionChecker) IsDM(i *discordgo.InteractionCreate) bool {
	if p.dmRoleID == "" {
		return true
	}
	if i.Member == nil {
		return false
	}
	return slices.Contains(i.Member.Roles, p.dmRoleID)
}
