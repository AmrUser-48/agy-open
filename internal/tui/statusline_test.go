package tui

import (
	"context"
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
	if !strings.HasPrefix(got, "⠙  ") {
		t.Fatalf("status = %q, want the second Braille spinner frame", got)
	}
	if !strings.Contains(got, ".") || strings.Contains(got, "Thinking") ||
		got[len(got)-1] < '0' || got[len(got)-1] > '9' {
		t.Fatalf("status = %q, want a decimal elapsed-seconds value", got)
	}
}

func TestSchedulerEscapeErasesCurrentOverlayBeforeClosing(t *testing.T) {
	ui := &UI{
		lineMode:       true,
		overlay:        &overlay{kind: "scheduler-home"},
		lineOverlayRows: 3,
	}
	ui.handleLineOverlay(context.Background(), "ESC")
	if ui.overlay != nil {
		t.Fatalf("scheduler home overlay should close, got %#v", ui.overlay)
	}
	if ui.lineOverlayRows != 0 {
		t.Fatalf("stale overlay row count = %d, want 0", ui.lineOverlayRows)
	}
}

func TestDisplayCellWidthCountsTerminalCells(t *testing.T) {
	if got := displayCellWidth("› nemotron-3-ultra-550b-a55b"); got != 28 {
		t.Fatalf("displayCellWidth() = %d, want 28", got)
	}
	if got := displayCellWidth("界"); got != 2 {
		t.Fatalf("wide rune cell width = %d, want 2", got)
	}
}
