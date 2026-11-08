package server

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConfigSidebarButtonIsWiredToReopenConfiguration(t *testing.T) {
	required := []string{
		`<button type="button" id="config-btn" title="Open Configuration">⚙ Config</button>`,
		`const configBtn = document.getElementById('config-btn');`,
		`function openConfiguration() {`,
		`sidebar.classList.remove('collapsed');`,
		`localStorage.setItem('agy_sidebar_collapsed', 'false');`,
		`const settings = sidebar.querySelector('.sidebar-settings');`,
		`configBtn.onclick = openConfiguration;`,
	}

	for _, fragment := range required {
		if !strings.Contains(htmlUI, fragment) {
			t.Errorf("web UI is missing config button behavior %q", fragment)
		}
	}
}


func TestPermissionUIDoesNotExposeCommandAllowlist(t *testing.T) {
	for _, forbidden := range []string{
		"Command allow rules",
		"approval-rules-list",
		"command-rule-input",
		"allow prefix",
		"date/grep/git/gh",
	} {
		if strings.Contains(htmlUI, forbidden) {
			t.Errorf("Web UI unexpectedly exposes command allowlist detail %q", forbidden)
		}
	}
	for _, mode := range []string{"disable", "enable", "ask", "autopilot"} {
		if !strings.Contains(htmlUI, `<option value="`+mode+`"`) {
			t.Errorf("Web UI is missing permission mode %q", mode)
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
		{name: "local API client without Origin", host: "127.0.0.1:8080", want: true},
		{name: "same origin", origin: "http://127.0.0.1:8080", host: "127.0.0.1:8080", want: true},
		{name: "different host", origin: "http://attacker.example", host: "127.0.0.1:8080", want: false},
		{name: "different port", origin: "http://127.0.0.1:9000", host: "127.0.0.1:8080", want: false},
		{name: "invalid origin", origin: "not a URL", host: "127.0.0.1:8080", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "http://"+test.host+"/api/settings", nil)
			req.Host = test.host
			if test.origin != "" {
				req.Header.Set("Origin", test.origin)
			}
			if got := sameOriginRequest(req); got != test.want {
				t.Fatalf("sameOriginRequest() = %v, want %v", got, test.want)
			}
		})
	}
}
