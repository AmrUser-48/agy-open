package tui

import "testing"

func TestOverlaySearchFiltersAndPreservesSelectionValues(t *testing.T) {
	u := &UI{overlay: &overlay{
		title: "Providers",
		kind: "config-choice",
		items: []string{"Google AI Studio", "Groq API", "NVIDIA API Catalog"},
		values: []string{"ai-studio", "groq", "nvidia"},
		settingKey: "modelProvider",
	}}
	if !u.handleOverlaySearchKey("RUNE:/") {
		t.Fatal("slash did not activate menu search")
	}
	for _, key := range []string{"RUNE:n", "RUNE:v", "RUNE:d"} {
		if !u.handleOverlaySearchKey(key) {
			t.Fatalf("%s was not consumed while search was active", key)
		}
	}
	if got := u.overlay.items; len(got) != 1 || got[0] != "NVIDIA API Catalog" {
		t.Fatalf("filtered items = %#v", got)
	}
	if got := u.overlay.values; len(got) != 1 || got[0] != "nvidia" {
		t.Fatalf("filtered values = %#v", got)
	}
	if !u.handleOverlaySearchKey("ENTER") || u.overlay.searchActive {
		t.Fatal("Enter did not finish search mode")
	}
	if u.overlay.values[0] != "nvidia" {
		t.Fatal("finishing search changed the selected value")
	}
	if !u.handleOverlaySearchKey("ESC") {
		t.Fatal("Escape did not clear an inactive filtered search")
	}
	if len(u.overlay.items) != 3 || u.overlay.values[1] != "groq" {
		t.Fatalf("clearing search did not restore the original menu: %#v", u.overlay)
	}
}

func TestOverlaySearchNoMatchesCannotSelectPlaceholder(t *testing.T) {
	u := &UI{overlay: &overlay{
		title: "Roles",
		kind: "agents",
		items: []string{"coder", "researcher"},
		values: []string{"coder", "researcher"},
	}}
	u.handleOverlaySearchKey("RUNE:/")
	for _, key := range []string{"RUNE:z", "RUNE:z", "RUNE:z"} {
		u.handleOverlaySearchKey(key)
	}
	if !u.overlay.searchEmpty || len(u.overlay.items) != 1 {
		t.Fatalf("empty result state = %#v", u.overlay)
	}
	if u.overlay.items[0] != "(no matches)" {
		t.Fatalf("empty result placeholder = %q", u.overlay.items[0])
	}
	if !u.handleOverlaySearchKey("ENTER") {
		t.Fatal("Enter should finish search even with no matches")
	}
	if !u.overlay.searchEmpty {
		t.Fatal("finishing search should preserve the empty result until cleared")
	}
	if !u.handleOverlaySearchKey("ESC") || len(u.overlay.items) != 2 {
		t.Fatal("Escape should restore entries when clearing the search")
	}
}

func TestOverlaySearchCanBeRefinedAndCancelled(t *testing.T) {
	u := &UI{overlay: &overlay{
		title: "Tools",
		items: []string{"read_file", "write_file", "search", "shell"},
	}}
	u.handleOverlaySearchKey("RUNE:/")
	u.handleOverlaySearchKey("RUNE:s")
	u.handleOverlaySearchKey("RUNE:h")
	if !u.overlay.searchActive || len(u.overlay.items) == 0 {
		t.Fatal("search mode did not produce candidates")
	}
	if !u.handleOverlaySearchKey("ESC") || u.overlay.searchActive {
		t.Fatal("Escape should leave search mode")
	}
	if len(u.overlay.items) != 4 {
		t.Fatalf("cancelled search left %d items, want 4", len(u.overlay.items))
	}
}
