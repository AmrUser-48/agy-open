package tui

import (
	"strings"
	"testing"
	"time"
)

func TestRequestTokenDeltaUsesProviderReportedSamples(t *testing.T) {
	tests := []struct {
		name                         string
		total, samples               int64
		previousTotal, previousSamples int64
		want                         string
	}{
		{name: "new reported tokens", total: 353, samples: 2, previousTotal: 40, previousSamples: 1, want: "313"},
		{name: "missing counter", total: 0, samples: 1, previousSamples: 1, want: "—"},
		{name: "first response without usage", want: "—"},
		{name: "reported zero", total: 0, samples: 1, want: "0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := requestTokenDelta(tt.total, tt.samples, tt.previousTotal, tt.previousSamples); got != tt.want {
				t.Fatalf("requestTokenDelta() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTrackRequestStatusOutputCountsWrappedRows(t *testing.T) {
	ui := &UI{
		requestStatusActive:   true,
		requestStatusDistance: 1,
		termCols:              5,
	}
	ui.trackRequestStatusOutputLocked("abcdef\nxy")
	if ui.requestStatusDistance != 3 || ui.requestStatusColumn != 2 {
		t.Fatalf("tracked position = row distance %d, column %d; want 3, 2",
			ui.requestStatusDistance, ui.requestStatusColumn)
	}
}

func TestTrackRequestStatusOutputIgnoresANSIAndCountsWideRunes(t *testing.T) {
	ui := &UI{
		requestStatusActive:   true,
		requestStatusDistance: 1,
		termCols:              8,
	}
	ui.trackRequestStatusOutputLocked("\x1b[31mabc\x1b[0m界")
	if ui.requestStatusDistance != 1 || ui.requestStatusColumn != 5 {
		t.Fatalf("tracked position = row distance %d, column %d; want 1, 5",
			ui.requestStatusDistance, ui.requestStatusColumn)
	}
}

func TestRequestStatusTextShowsElapsedSecondsAndAnimationFrame(t *testing.T) {
	ui := &UI{
		requestStarted:      time.Now().Add(-1600 * time.Millisecond),
		requestStatusFrame:  1,
	}
	got := ui.requestStatusText(false)
	if !strings.HasPrefix(got, "..  ") {
		t.Fatalf("status = %q, want the second animation frame", got)
	}
	if !strings.Contains(got, "s") || strings.Contains(got, "Thinking") {
		t.Fatalf("status = %q, want elapsed seconds instead of Thinking", got)
	}
}

func TestDisplayCellWidthCountsTerminalCells(t *testing.T) {
	if got := displayCellWidth("› nemotron-3-ultra-550b-a55b"); got != 29 {
		t.Fatalf("displayCellWidth() = %d, want 29", got)
	}
	if got := displayCellWidth("界"); got != 2 {
		t.Fatalf("wide rune cell width = %d, want 2", got)
	}
}
