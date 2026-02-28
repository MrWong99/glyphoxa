package commands

import "testing"

func TestDetectFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		filename string
		want     AttachmentFormat
	}{
		{"campaign.yaml", FormatYAML},
		{"campaign.yml", FormatYAML},
		{"CAMPAIGN.YAML", FormatYAML},
		{"world.json", FormatJSON},
		{"export.JSON", FormatJSON},
		{"readme.txt", FormatUnknown},
		{"image.png", FormatUnknown},
		{"noext", FormatUnknown},
		{"", FormatUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			t.Parallel()
			got := DetectFormat(tt.filename)
			if got != tt.want {
				t.Errorf("DetectFormat(%q) = %v, want %v", tt.filename, got, tt.want)
			}
		})
	}
}

func TestAttachmentFormatString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		format AttachmentFormat
		want   string
	}{
		{FormatYAML, "yaml"},
		{FormatJSON, "json"},
		{FormatUnknown, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			if got := tt.format.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFirstAttachment_NoAttachments(t *testing.T) {
	t.Parallel()

	i := testInteractionWithRoles("dm-role")
	if att := FirstAttachment(i); att != nil {
		t.Errorf("expected nil, got %v", att)
	}
}
