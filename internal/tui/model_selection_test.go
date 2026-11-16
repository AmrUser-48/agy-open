package tui

import "testing"

func TestValidCustomModelID(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{id: "gemini-3-flash-agent", want: true},
		{id: "claude-opus-5-5-high", want: true},
		{id: "models/gemini-3-flash", want: true},
		{id: "gemini_custom.v2", want: true},
		{id: "", want: false},
		{id: " gemini-3-flash", want: false},
		{id: "gemini 3 flash", want: false},
		{id: "gemini-3-flash?debug=true", want: false},
		{id: "gemini-3-flash\\n", want: false},
		{id: string(make([]byte, 257)), want: false},
	}
	for _, tt := range tests {
		if got := validCustomModelID(tt.id); got != tt.want {
			t.Errorf("validCustomModelID(%q) = %t, want %t", tt.id, got, tt.want)
		}
	}
}
