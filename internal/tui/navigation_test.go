package tui

import (
	"context"
	"testing"
)

func selectConfigGroup(t *testing.T, u *UI, id string) {
	t.Helper()
	if u.overlay == nil {
		t.Fatal("configuration menu is missing")
	}
	for i, value := range u.overlay.values {
		if value == id {
			u.selectSettingsOverlay(i, context.Background())
			return
		}
	}
	t.Fatalf("configuration group %q is missing from %#v", id, u.overlay.values)
}

func selectConfigField(t *testing.T, u *UI, key string) int {
	t.Helper()
	if u.overlay == nil {
		t.Fatal("configuration submenu is missing")
	}
	for i, value := range u.overlay.keys {
		if value == key {
			return i
		}
	}
	t.Fatalf("configuration field %q is missing from %#v", key, u.overlay.keys)
	return -1
}

func TestConfigurationUsesNestedProviderAndRateLimitMenus(t *testing.T) {
	a, _ := newResumeCompletionAgent(t, "20261114T120010.000Z")
	u := &UI{agent: a, cfg: a.Config(), history: newPromptHistory(nil)}

	u.openSettings()
	root := u.overlay
	if root == nil || root.kind != "settings" {
		t.Fatalf("configuration root = %#v", root)
	}
	selectConfigGroup(t, u, "rate-limits")
	rates := u.overlay
	if rates == nil || rates.settingKey != "rate-limits" || rates.parent != root {
		t.Fatalf("rate-limit submenu = %#v; parent = %#v", rates, root)
	}
	selectConfigGroup(t, u, "nvidia")
	nvidia := u.overlay
	if nvidia == nil || nvidia.settingKey != "nvidia" || nvidia.parent != rates {
		t.Fatalf("NVIDIA submenu = %#v; parent = %#v", nvidia, rates)
	}
	for _, key := range []string{"nvidiaRPM", "nvidiaTPM", "nvidiaMaxOutputTokens", "nvidiaRetryOn429", "nvidiaMaxBackoff"} {
		selectConfigField(t, u, key)
	}
	u.backOverlay()
	u.backOverlay()
	selectConfigGroup(t, u, "groq")
	for _, key := range []string{"groqRPM", "groqTPM", "groqMaxOutputTokens", "groqRetryOn429", "groqMaxBackoff"} {
		selectConfigField(t, u, key)
	}
}

func TestConfiguratorEscapeReturnsOneMenuLevelAtATime(t *testing.T) {
	a, _ := newResumeCompletionAgent(t, "20261114T120011.000Z")
	u := &UI{agent: a, cfg: a.Config(), history: newPromptHistory(nil)}

	u.openSettings()
	root := u.overlay
	if root == nil || root.kind != "settings" {
		t.Fatalf("configuration root = %#v", root)
	}
	selectConfigGroup(t, u, "provider")
	providerGroup := u.overlay
	if providerGroup == nil || providerGroup.kind != "settings-category" || providerGroup.parent != root {
		t.Fatalf("provider section = %#v; parent = %#v", providerGroup, root)
	}
	u.changeSetting(selectConfigField(t, u, "credentials"), context.Background())
	providers := u.overlay
	if providers == nil || providers.kind != "credential-providers" || providers.parent != providerGroup {
		t.Fatalf("provider menu = %#v; parent = %#v", providers, providerGroup)
	}

	u.openCredentialActions("google-api")
	actions := u.overlay
	if actions == nil || actions.kind != "credential-actions" || actions.parent != providers {
		t.Fatalf("provider actions = %#v; parent = %#v", actions, providers)
	}

	u.backOverlay()
	if u.overlay == nil || u.overlay.kind != "credential-providers" {
		t.Fatalf("first Escape returned %#v, want provider list", u.overlay)
	}
	u.backOverlay()
	if u.overlay == nil || u.overlay.kind != "settings-category" || u.overlay.settingKey != "provider" {
		t.Fatalf("second Escape returned %#v, want Provider & credentials", u.overlay)
	}
	u.backOverlay()
	if u.overlay == nil || u.overlay.kind != "settings" {
		t.Fatalf("third Escape returned %#v, want Configuration", u.overlay)
	}
	u.backOverlay()
	if u.overlay != nil {
		t.Fatalf("fourth Escape should close Configuration, got %#v", u.overlay)
	}
}

func TestCredentialEditorEscapeReturnsToProviderActions(t *testing.T) {
	a, _ := newResumeCompletionAgent(t, "20261114T120012.000Z")
	u := &UI{agent: a, cfg: a.Config(), history: newPromptHistory(nil)}

	u.openSettings()
	selectConfigGroup(t, u, "provider")
	providerGroup := u.overlay
	u.changeSetting(selectConfigField(t, u, "credentials"), context.Background())
	u.openCredentialActions("google-api")
	actions := u.overlay
	if actions == nil || actions.kind != "credential-actions" {
		t.Fatalf("credential actions = %#v", actions)
	}

	u.runCredentialAction("google-api", "set", context.Background())
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
	if u.overlay.parent.parent != providerGroup {
		t.Fatalf("provider list parent = %#v, want provider section", u.overlay.parent.parent)
	}
}

func TestLineOverlayEscapeNavigatesToParent(t *testing.T) {
	a, _ := newResumeCompletionAgent(t, "20261114T120013.000Z")
	parent := &overlay{title: "Configuration", kind: "settings"}
	child := &overlay{title: "Provider credentials", kind: "credential-providers", parent: parent}
	u := &UI{agent: a, cfg: a.Config(), col: newColors(a.Config()), lineMode: true, overlay: child, termCols: 80, termRows: 24, history: newPromptHistory(nil)}
	u.handleLineOverlay(context.Background(), "ESC")
	if u.overlay == nil || u.overlay.kind != "settings" {
		t.Fatalf("scrollback Escape returned %#v, want Configuration", u.overlay)
	}
}

func TestFullScreenOverlayEscapeNavigatesToParent(t *testing.T) {
	a, _ := newResumeCompletionAgent(t, "20261114T120014.000Z")
	parent := &overlay{title: "Configuration", kind: "settings"}
	child := &overlay{title: "Provider credentials", kind: "credential-providers", parent: parent}
	u := &UI{agent: a, cfg: a.Config(), col: newColors(a.Config()), overlay: child, termCols: 80, termRows: 24, history: newPromptHistory(nil)}
	u.handleOverlay(context.Background(), "ESC")
	if u.overlay == nil || u.overlay.kind != "settings" {
		t.Fatalf("full-screen Escape returned %#v, want Configuration", u.overlay)
	}
}
