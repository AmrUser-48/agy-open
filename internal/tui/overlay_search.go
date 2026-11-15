package tui

import (
	"fmt"
	"strings"
)

func overlaySearchTitle(o *overlay) string {
	if o == nil || strings.TrimSpace(o.searchQuery) == "" {
		return o.title
	}
	return o.title + "  /" + o.searchQuery
}

func overlaySearchFooter(o *overlay) string {
	if o == nil {
		return ""
	}
	switch {
	case o.searchActive:
		return "search · type to filter · Enter finish · Esc clear"
	case o.searchItems != nil:
		return fmt.Sprintf("%d matches · / refine · Esc clear · Enter select", len(o.items))
	default:
		return o.footer
	}
}

// handleOverlaySearchKey implements the same / search interaction for all
// ordinary selection menus. The model picker keeps its provider-specific filter.
func (u *UI) handleOverlaySearchKey(key string) bool {
	o := u.overlay
	if o == nil || o.kind == "models" || len(o.items) == 0 {
		return false
	}

	if key == "RUNE:/" && !o.searchActive {
		if o.searchItems == nil {
			o.searchItems = append([]string(nil), o.items...)
			o.searchValues = append([]string(nil), o.values...)
			o.searchKeys = append([]string(nil), o.keys...)
		}
		o.searchActive = true
		o.searchQuery = ""
		o.searchEmpty = false
		u.applyOverlaySearch()
		return true
	}

	if key == "ESC" {
		if o.searchActive || o.searchItems != nil {
			o.searchActive = false
			o.searchQuery = ""
			u.clearOverlaySearch()
			return true
		}
		return false
	}

	if !o.searchActive {
		return false
	}

	switch {
	case key == "ENTER":
		o.searchActive = false
		return true
	case strings.HasPrefix(key, "RUNE:"):
		runes := []rune(strings.TrimPrefix(key, "RUNE:"))
		if len(runes) == 0 || runes[0] < 0x20 || runes[0] == 0x7f {
			return false
		}
		if len([]rune(o.searchQuery))+len(runes) > 128 {
			return true
		}
		o.searchQuery += string(runes)
	case key == "BACKSPACE" || key == "CTRL-H":
		runes := []rune(o.searchQuery)
		if len(runes) > 0 {
			o.searchQuery = string(runes[:len(runes)-1])
		}
	case key == "CTRL-U":
		o.searchQuery = ""
	case key == "CTRL-W":
		query := strings.TrimRight(o.searchQuery, " \t")
		if cut := strings.LastIndexAny(query, " \t"); cut >= 0 {
			o.searchQuery = query[:cut+1]
		} else {
			o.searchQuery = ""
		}
	default:
		return false
	}
	u.applyOverlaySearch()
	return true
}

func (u *UI) applyOverlaySearch() {
	o := u.overlay
	if o == nil || o.searchItems == nil {
		return
	}
	if strings.TrimSpace(o.searchQuery) == "" {
		u.clearOverlaySearch()
		return
	}

	indices := fuzzyRank(o.searchItems, o.searchQuery)
	items := make([]string, 0, len(indices))
	var values, keys []string
	if o.searchValues != nil {
		values = make([]string, 0, len(indices))
	}
	if o.searchKeys != nil {
		keys = make([]string, 0, len(indices))
	}
	for _, index := range indices {
		items = append(items, o.searchItems[index])
		if o.searchValues != nil && index < len(o.searchValues) {
			values = append(values, o.searchValues[index])
		}
		if o.searchKeys != nil && index < len(o.searchKeys) {
			keys = append(keys, o.searchKeys[index])
		}
	}
	o.searchEmpty = len(items) == 0
	if o.searchEmpty {
		items = []string{"(no matches)"}
	}
	o.items = items
	o.values = values
	o.keys = keys
	o.index = 0
}

func (u *UI) clearOverlaySearch() {
	o := u.overlay
	if o == nil || o.searchItems == nil {
		return
	}
	o.items = append([]string(nil), o.searchItems...)
	o.values = append([]string(nil), o.searchValues...)
	o.keys = append([]string(nil), o.searchKeys...)
	o.searchItems = nil
	o.searchValues = nil
	o.searchKeys = nil
	o.searchQuery = ""
	o.searchActive = false
	o.searchEmpty = false
	o.index = 0
}
