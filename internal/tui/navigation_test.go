package tui

import (
	"context"
	"testing"
)

func TestConfiguratorEscapeReturnsOneMenuLevelAtATime(t *testing.T) {
	a, _ := newResumeCompletionAgent(t, "20261114T120010.000Z")
	u := &UI{agent: a, cfg: a.Config(), history: newPromptHistory(nil)}

	u.openSettings()
	root := u.overlay
	if root == nil || root.kind != "settings" {
		t.Fatalf("configuration root = %#v", root)
	}
	credentialIndex := -1
	for i, key := range root.keys {
		if key == "credentials" {
			credentialIndex = i
			break
		}
	}
	if credentialIndex < 0 {
		t.Fatal("Provider credentials setting is missing")
	}
	u.changeSetting(credentialIndex, context.Background())
	providers := u.overlay
	if providers == nil || providers.kind != "credential-providers" || providers.parent != root {
		t.Fatalf("provider menu = %#v; parent = %#v", providers, root)
	}

	u.openCredentialActions("gemini-api")
	actions := u.overlay
	if actions == nil || actions.kind != "credential-actions" || actions.parent != providers {
		t.Fatalf("provider actions = %#v; parent = %#v", actions, providers)
	}

	u.backOverlay()
	if u.overlay == nil || u.overlay.kind != "credential-providers" {
		t.Fatalf("first Escape returned %#v, want provider list", u.overlay)
	}
	u.backOverlay()
	if u.overlay == nil || u.overlay.kind != "settings" {
		t.Fatalf("second Escape returned %#v, want Configuration", u.overlay)
	}
	u.backOverlay()
	if u.overlay != nil {
		t.Fatalf("third Escape should close Configuration, got %#v", u.overlay)
	}
}

func TestCredentialEditorEscapeReturnsToProviderActions(t *testing.T) {
	a, _ := newResumeCompletionAgent(t, "20261114T120011.000Z")
	u := &UI{agent: a, cfg: a.Config(), history: newPromptHistory(nil)}

	u.openSettings()
	root := u.overlay
	for i, key := range root.keys {
		if key == "credentials" {
			u.changeSetting(i, context.Background())
			break
		}
	}
	u.openCredentialActions("gemini-api")
	actions := u.overlay
	if actions == nil || actions.kind != "credential-actions" {
		t.Fatalf("credential actions = %#v", actions)
	}

	u.runCredentialAction("gemini-api", "set", context.Background())
	if u.configEdit == nil || u.configEdit.parent != actions {
		t.Fatalf("credential editor did not retain action menu: %#v", u.configEdit)
	}
	u.handleConfigEditKey("ESC")
	if u.configEdit != nil {
		t.Fatal("credential editor remained active after Escape")
	}
	if u.overlay == nil || u.overlay.kind != "credential-actions" {
		t.Fatalf("Escape from API-key editor returned %#v, want provider actions", u.overlay)
	}
	if u.overlay.parent == nil || u.overlay.parent.kind != "credential-providers" {
		t.Fatalf("credential action parent = %#v, want provider list", u.overlay.parent)
	}
}

func TestLineOverlayEscapeNavigatesToParent(t *testing.T) {
	a, _ := newResumeCompletionAgent(t, "20261114T120012.000Z")
	parent := &overlay{title: "Configuration", kind: "settings"}
	child := &overlay{title: "Provider credentials", kind: "credential-providers", parent: parent}
	u := &UI{agent: a, cfg: a.Config(), col: newColors(a.Config()), lineMode: true, overlay: child, termCols: 80, termRows: 24, history: newPromptHistory(nil)}
	u.handleLineOverlay(context.Background(), "ESC")
	if u.overlay == nil || u.overlay.kind != "settings" {
		t.Fatalf("scrollback Escape returned %#v, want Configuration", u.overlay)
	}
}

func TestFullScreenOverlayEscapeNavigatesToParent(t *testing.T) {
	a, _ := newResumeCompletionAgent(t, "20261114T120013.000Z")
	parent := &overlay{title: "Configuration", kind: "settings"}
	child := &overlay{title: "Provider credentials", kind: "credential-providers", parent: parent}
	u := &UI{agent: a, cfg: a.Config(), col: newColors(a.Config()), overlay: child, termCols: 80, termRows: 24, history: newPromptHistory(nil)}
	u.handleOverlay(context.Background(), "ESC")
	if u.overlay == nil || u.overlay.kind != "settings" {
		t.Fatalf("full-screen Escape returned %#v, want Configuration", u.overlay)
	}
}
