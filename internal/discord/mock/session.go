// Package mock provides test doubles for Discord interaction testing.
package mock

import "github.com/bwmarrin/discordgo"

// InteractionResponder records interaction responses for test assertions.
type InteractionResponder struct {
	// Responses records all InteractionRespond calls.
	Responses []*discordgo.InteractionResponse

	// FollowUps records all FollowupMessageCreate calls.
	FollowUps []*discordgo.WebhookParams

	// Err is returned by InteractionRespond and FollowupMessageCreate
	// when non-nil, allowing error injection.
	Err error
}

// InteractionRespond records the response and returns the configured error.
func (m *InteractionResponder) InteractionRespond(i *discordgo.Interaction, resp *discordgo.InteractionResponse) error {
	m.Responses = append(m.Responses, resp)
	return m.Err
}

// FollowupMessageCreate records the follow-up and returns a stub message.
func (m *InteractionResponder) FollowupMessageCreate(i *discordgo.Interaction, wait bool, params *discordgo.WebhookParams) (*discordgo.Message, error) {
	m.FollowUps = append(m.FollowUps, params)
	if m.Err != nil {
		return nil, m.Err
	}
	return &discordgo.Message{ID: "mock-followup"}, nil
}

// LastResponse returns the most recently recorded response, or nil.
func (m *InteractionResponder) LastResponse() *discordgo.InteractionResponse {
	if len(m.Responses) == 0 {
		return nil
	}
	return m.Responses[len(m.Responses)-1]
}

// LastFollowUp returns the most recently recorded follow-up, or nil.
func (m *InteractionResponder) LastFollowUp() *discordgo.WebhookParams {
	if len(m.FollowUps) == 0 {
		return nil
	}
	return m.FollowUps[len(m.FollowUps)-1]
}

// Reset clears all recorded interactions and errors.
func (m *InteractionResponder) Reset() {
	m.Responses = nil
	m.FollowUps = nil
	m.Err = nil
}
