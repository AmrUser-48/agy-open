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


func TestModelPickerVimNavigationAndSlashFilterMode(t *testing.T) {
	o := &overlay{
		kind:   "models",
		items:  []string{"Model A", "Model B", "Model C"},
		values: []string{"model-a", "model-b", "model-c"},
	}
	u := &UI{overlay: o}

	for _, step := range []struct {
		key  string
		want int
	}{
		{key: "RUNE:j", want: 1},
		{key: "RUNE:j", want: 2},
		{key: "RUNE:k", want: 1},
		{key: "DOWN", want: 2},
		{key: "UP", want: 1},
	} {
		if !u.handleModelPickerKey(step.key) {
			t.Fatalf("%s was not consumed by the model picker", step.key)
		}
		if o.index != step.want {
			t.Fatalf("after %s index = %d, want %d", step.key, o.index, step.want)
		}
	}
	if o.filter != "" || o.filterActive {
		t.Fatalf("navigation modified filter state: filter=%q active=%t", o.filter, o.filterActive)
	}

	if !u.handleModelPickerKey("RUNE:/") || !o.filterActive {
		t.Fatal("slash did not enter model filter mode")
	}
	selected := o.index
	if !u.handleModelPickerKey("ENTER") || o.filterActive {
		t.Fatal("Enter did not finish model filter mode")
	}
	if o.index != selected {
		t.Fatalf("finishing filter moved selection from %d to %d", selected, o.index)
	}
	if !u.handleModelPickerKey("RUNE:j") || o.index != selected+1 {
		t.Fatalf("j after finishing filter did not navigate: index=%d, want %d", o.index, selected+1)
	}
	if !u.handleModelPickerKey("RUNE:k") || o.index != selected {
		t.Fatalf("k after finishing filter did not navigate back: index=%d, want %d", o.index, selected)
	}
}
