package summary

import "testing"

func TestStripANSI(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain text unchanged",
			input: "hello world",
			want:  "hello world",
		},
		{
			name:  "color codes stripped",
			input: "\x1b[32mgreen\x1b[0m text",
			want:  "green text",
		},
		{
			name:  "bold stripped",
			input: "\x1b[1mbold\x1b[0m",
			want:  "bold",
		},
		{
			name:  "cursor movement stripped",
			input: "\x1b[2Jhello\x1b[H",
			want:  "hello",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "complex SGR params",
			input: "\x1b[38;5;196mred\x1b[0m",
			want:  "red",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripANSI(tt.input)
			if got != tt.want {
				t.Errorf("StripANSI(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
