package server

import (
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
