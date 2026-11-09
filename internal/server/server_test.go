package server

import (
	"strings"
	"testing"
)

func TestInteractiveWebUIHasNoExternalMarkdownDependencies(t *testing.T) {
	for _, dependency := range []string{
		"cdn.jsdelivr.net",
		"cdnjs.cloudflare.com",
		"marked.",
		"hljs.",
	} {
		if strings.Contains(htmlUI, dependency) {
			t.Errorf("interactive UI still references external dependency %q", dependency)
		}
	}
	for _, required := range []string{
		"function renderMarkdown",
		"function appendInline",
		"contentDiv.textContent = text",
		"noopener noreferrer",
		"@media (max-width: 760px)",
		"sidebar-backdrop",
		"--font-ui",
		"id=\"web-background-color\"",
		"id=\"web-foreground-color\"",
		"id=\"web-accent-color\"",
		"id=\"web-font-select\"",
		"color-mix(in srgb",
	} {
		if !strings.Contains(htmlUI, required) {
			t.Errorf("interactive UI is missing expected safe/minimal feature %q", required)
		}
	}
}

func TestPlainChatAndApprovalPagesRequireNoJavaScript(t *testing.T) {
	for name, page := range map[string]string{
		"plain chat": plainUI,
		"plain approval": plainApprovalUI,
	} {
		if strings.Contains(strings.ToLower(page), "<script") {
			t.Errorf("%s page must not contain JavaScript", name)
		}
		if !strings.Contains(page, "<form") || !strings.Contains(page, "method=\"post\"") {
			t.Errorf("%s page must use ordinary HTML form submission", name)
		}
	}
	if !strings.Contains(plainUI, "action=\"/plain\"") {
		t.Fatal("plain chat form does not submit to /plain")
	}
	for _, required := range []string{
		"Allow once",
		"Deny",
		"{{.Target}}",
		"{{.Action}}",
		"one-time approval only",
	} {
		if !strings.Contains(plainApprovalUI, required) {
			t.Errorf("plain approval page missing %q", required)
		}
	}
}

func TestWebAppearanceValidation(t *testing.T) {
	for _, color := range []string{"#000000", "#aBcD09", "#FFFFFF"} {
		if !validWebHexColor(color) { t.Errorf("expected valid color %q", color) }
	}
	for _, color := range []string{"", "#fff", "000000", "#gggggg", "#00000000"} {
		if validWebHexColor(color) { t.Errorf("expected invalid color %q", color) }
	}
	for _, font := range []string{"system", "sans", "mono", "terminus"} {
		if !validWebFont(font) { t.Errorf("expected valid web font %q", font) }
	}
	if validWebFont("comic-sans") { t.Fatal("unsupported web font was accepted") }
}


func TestInteractiveWebUIExposesTerminalSandboxAndLifecycleApproval(t *testing.T) {
	for _, required := range []string{
		"terminal-sandbox-checkbox",
		"enableTerminalSandbox",
		"oneTimeOnly",
		"RequiresLifecycleApproval",
		"Bubblewrap isolates shell commands only",
	} {
		if !strings.Contains(htmlUI, required) {
			t.Errorf("interactive UI is missing sandbox/lifecycle feature %q", required)
		}
	}
}
