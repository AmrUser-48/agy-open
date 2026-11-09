package server

import (
	"encoding/json"
	"net/http/httptest"
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
		"Bubblewrap isolates shell commands only",
	} {
		if !strings.Contains(htmlUI, required) {
			t.Errorf("interactive UI is missing sandbox/lifecycle feature %q", required)
		}
	}
}


func TestSameOriginRequest(t *testing.T) {
	tests := []struct {
		name   string
		origin string
		host   string
		want   bool
	}{
		{name: "no origin for local client", host: "127.0.0.1:8080", want: true},
		{name: "same http origin", origin: "http://127.0.0.1:8080", host: "127.0.0.1:8080", want: true},
		{name: "same https origin", origin: "https://agy.example.test", host: "agy.example.test", want: true},
		{name: "cross origin", origin: "https://evil.example", host: "127.0.0.1:8080", want: false},
		{name: "different port", origin: "http://127.0.0.1:9000", host: "127.0.0.1:8080", want: false},
		{name: "null origin", origin: "null", host: "127.0.0.1:8080", want: false},
		{name: "origin with path", origin: "http://127.0.0.1:8080/evil", host: "127.0.0.1:8080", want: false},
		{name: "origin with credentials", origin: "http://user@127.0.0.1:8080", host: "127.0.0.1:8080", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://"+tt.host+"/api/approval/respond", nil)
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}
			if got := sameOriginRequest(r); got != tt.want {
				t.Fatalf("sameOriginRequest() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInteractiveWebUIExposesPermissionPoliciesAndResponseApprovals(t *testing.T) {
	for _, required := range []string{
		"disable all tools",
		"read-only · list/read/search",
		"workspace read/write · restricted commands",
		"ask for every tool operation",
		"autopilot · unrestricted",
		"allowResponse",
		"denyResponse",
		"lifecycle",
	} {
		if !strings.Contains(htmlUI, required) {
			t.Errorf("interactive UI is missing permission feature %q", required)
		}
	}
}


func TestInteractiveWebUIAllowsUnlimitedTurns(t *testing.T) {
	if !strings.Contains(htmlUI, `id="max-turns-input" min="0" max="500"`) {
		t.Fatal("Web UI must allow maxTurns=0")
	}
}


func TestWebUIRestoresToolActivityAndConfigNavigation(t *testing.T) {
	for _, required := range []string{
		`id="config-btn"`,
		`const configBtn = document.getElementById('config-btn')`,
		`configBtn.onclick = openConfiguration;`,
		`setSidebarOpen(true);`,
		`settings.scrollIntoView({ behavior: 'smooth', block: 'start' });`,
		"function appendHistoryItem(m)",
		"m.kind === 'tool_call'",
		"m.kind === 'tool_approval'",
		"details.className = 'tool-history'",
		"args.textContent = 'Arguments: ' + m.arguments",
		"output.textContent = m.output",
		"appendHistoryItem(m)",
	} {
		if !strings.Contains(htmlUI, required) {
			t.Errorf("interactive UI is missing tool-history/config behavior %q", required)
		}
	}
}


func TestPlainChatRendersStructuredToolHistory(t *testing.T) {
	for _, required := range []string{
		`eq .Kind "tool_call"`,
		`eq .Kind "tool_approval"`,
		"TOOL · {{.Tool}}",
		"APPROVAL · {{.Decision}}",
		"{{.Arguments}}",
		"{{.Output}}",
	} {
		if !strings.Contains(plainUI, required) {
			t.Errorf("plain UI is missing structured history field %q", required)
		}
	}
}


func TestLiveToolActivityStreamsExactShellCommand(t *testing.T) {
	for _, required := range []string{
		"function addToolActivity(event)",
		"event.command",
		"event.callId",
		"data.toolPhase",
		"m.command",
		"{{.Command}}",
		"$ {{.Command}}",
	} {
		if !strings.Contains(htmlUI+plainUI, required) {
			t.Errorf("tool activity UI is missing %q", required)
		}
	}

	payload, err := json.Marshal(StreamEvent{
		ToolPhase: "end", Tool: "shell", Command: "make oldconfig",
		DurationMS: 655700,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"\"toolPhase\":\"end\"", "\"command\":\"make oldconfig\"", "\"durationMs\":655700"} {
		if !strings.Contains(string(payload), required) {
			t.Errorf("stream event JSON is missing %q: %s", required, payload)
		}
	}
}

func TestPendingApprovalResponseMarksOnlyRiskyShellCommandsOneTime(t *testing.T) {
	cases := []struct {
		action, target string
		want           bool
	}{
		{"shell", "git status --short", false},
		{"shell", "git commit -m test", true},
		{"shell", "sudo reboot", true},
		{"write_file", "src/main.go", false},
	}
	for _, tc := range cases {
		got := pendingApprovalResponse(tc.action, tc.target)
		if got["oneTimeOnly"] != tc.want {
			t.Errorf("%s %q oneTimeOnly = %#v, want %v", tc.action, tc.target, got["oneTimeOnly"], tc.want)
		}
	}
	if got := pendingApprovalResponse("shell", "sudo reboot"); got["lifecycle"] != true {
		t.Fatalf("lifecycle flag = %#v, want true", got["lifecycle"])
	}
}
