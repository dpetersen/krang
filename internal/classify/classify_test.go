package classify

import (
	"encoding/json"
	"testing"
)

func TestResultParsing(t *testing.T) {
	tests := []struct {
		name          string
		json          string
		wantAttention bool
		wantErr       bool
	}{
		{
			name:          "needs attention",
			json:          `{"needs_attention": true}`,
			wantAttention: true,
		},
		{
			name:          "does not need attention",
			json:          `{"needs_attention": false}`,
			wantAttention: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result Result
			if err := json.Unmarshal([]byte(tt.json), &result); err != nil {
				t.Fatalf("unexpected parse error: %v", err)
			}
			if result.NeedsAttention != tt.wantAttention {
				t.Errorf("NeedsAttention = %v, want %v", result.NeedsAttention, tt.wantAttention)
			}
		})
	}
}

func TestEnvelopeParsing(t *testing.T) {
	raw := `{"structured_output":{"needs_attention":true},"result":"","is_error":false}`

	var envelope struct {
		StructuredOutput Result `json:"structured_output"`
		Result           string `json:"result"`
		IsError          bool   `json:"is_error"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if !envelope.StructuredOutput.NeedsAttention {
		t.Error("expected NeedsAttention to be true")
	}
}
