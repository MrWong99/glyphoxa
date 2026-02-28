package commands

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// AttachmentFormat identifies the detected file format of a Discord attachment.
type AttachmentFormat int

const (
	// FormatUnknown means the file extension was not recognised.
	FormatUnknown AttachmentFormat = iota

	// FormatYAML indicates a .yaml or .yml file.
	FormatYAML

	// FormatJSON indicates a .json file.
	FormatJSON
)

// String returns a human-readable label for the format.
func (f AttachmentFormat) String() string {
	switch f {
	case FormatYAML:
		return "yaml"
	case FormatJSON:
		return "json"
	default:
		return "unknown"
	}
}

// DownloadedAttachment holds the result of downloading a Discord file attachment.
type DownloadedAttachment struct {
	// Body is the response body, limited to maxImportSize+1 bytes.
	// The caller is responsible for closing it.
	Body io.ReadCloser

	// Filename is the original filename from Discord.
	Filename string

	// Format is the detected file format based on extension.
	Format AttachmentFormat

	// Size is the attachment size as reported by Discord (before download).
	Size int
}

// DetectFormat returns the AttachmentFormat based on a filename's extension.
func DetectFormat(filename string) AttachmentFormat {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".yaml", ".yml":
		return FormatYAML
	case ".json":
		return FormatJSON
	default:
		return FormatUnknown
	}
}

// FirstAttachment extracts the first attachment from an interaction's resolved
// data. Returns nil if no attachments are present or the interaction is not
// an application command.
func FirstAttachment(i *discordgo.InteractionCreate) *discordgo.MessageAttachment {
	if i.Type != discordgo.InteractionApplicationCommand {
		return nil
	}
	data := i.ApplicationCommandData()
	if data.Resolved == nil || len(data.Resolved.Attachments) == 0 {
		return nil
	}
	for _, a := range data.Resolved.Attachments {
		return a
	}
	return nil
}

// DownloadAttachment downloads a Discord attachment with the given context.
// The returned body is limited to maxImportSize+1 bytes. The caller must
// close the returned DownloadedAttachment.Body when done.
func DownloadAttachment(ctx context.Context, attachment *discordgo.MessageAttachment) (*DownloadedAttachment, error) {
	if attachment == nil {
		return nil, fmt.Errorf("attachment is nil")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, attachment.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("create download request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download attachment: %w", err)
	}

	return &DownloadedAttachment{
		Body: struct {
			io.Reader
			io.Closer
		}{io.LimitReader(resp.Body, maxImportSize+1), resp.Body},
		Filename: attachment.Filename,
		Format:   DetectFormat(attachment.Filename),
		Size:     attachment.Size,
	}, nil
}
