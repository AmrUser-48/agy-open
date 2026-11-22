package tui

import (
	"context"
	"fmt"
	"strings"
)

func (u *UI) openTeamworkHome() {
	u.overlay = &overlay{
		title: "Team workspace",
		items: []string{
			"Start coordinated work · plan and run a multi-step task",
			"Preview a plan · inspect the proposed work without changing files",
			"Recover interrupted work · review isolated worker changes",
			"How coordinated work is handled",
		},
		values: []string{"run", "preview", "recover", "help"},
		kind: "teamwork-home",
		footer: "Enter choose · Esc close",
	}
	if u.lineMode { u.clearPrompt(); u.renderLineOverlay() } else { u.render() }
}

func (u *UI) selectTeamworkHome(index int) {
	if u.overlay == nil || index < 0 || index >= len(u.overlay.values) { return }
	switch u.overlay.values[index] {
	case "run":
		u.beginTeamworkInput("run")
	case "preview":
		u.beginTeamworkInput("preview")
	case "recover":
		u.openTeamworkRecoveries()
	case "help":
		u.overlay = &overlay{
			title: "Coordinated work",
			items: []string{
				"AGY first proposes bounded tasks and dependencies.",
				"Independent tasks run together; dependent tasks wait for prerequisites.",
				"Only one worker may write, and its patch is reviewed before integration.",
				"Use Preview first when you want to inspect the plan before execution.",
				"Worker progress and results remain visible in this TUI conversation.",
			},
			kind: "info",
			footer: "Esc back to Team workspace",
			parent: u.overlay,
		}
	}
}

func (u *UI) beginTeamworkInput(mode string) {
	if u.lineMode { u.eraseLineOverlay() }
	u.overlay = nil
	label := "Task for the team"
	hint := "Describe the outcome you want. AGY will propose bounded tasks and dependencies."
	if mode == "preview" {
		label = "Task to plan"
		hint = "Describe the work. AGY will validate and show a plan without starting workers or changing files."
	}
	u.configEdit = &settingEdit{key: "teamwork:"+mode, label: label, hint: hint}
	u.input = nil
	u.cursor = 0
	u.completionActive = false
	u.termWrite("\n"+u.col.bold+label+u.col.reset+"\n")
	u.termWrite(u.col.dim+hint+u.col.reset+"\n")
	u.termWrite(u.col.dim+"Enter submit · Shift+Enter newline · Esc cancel · Ctrl+V paste"+u.col.reset+"\n")
	u.renderLinePrompt()
}

func (u *UI) startTeamworkMode(parent context.Context, mode, task string) {
	task = strings.TrimSpace(task)
	if task == "" { u.beginTeamworkInput(mode); return }
	prefix := "/teamwork "
	if mode == "preview" { prefix = "/teamwork-preview " }
	u.startAgent(parent, prefix+task)
}

func (u *UI) openTeamworkRecoveries() {
	recoveries, err := u.agent.TeamworkRecoveries()
	if err != nil {
		u.notifyMessage("error", err.Error())
		return
	}
	parent := u.overlay
	items := make([]string, 0, len(recoveries))
	values := make([]string, 0, len(recoveries))
	for _, item := range recoveries {
		items = append(items, fmt.Sprintf("%s · %s · %s · %s", item.ID, item.Role, item.Status, item.Task))
		values = append(values, item.ID)
	}
	if len(items) == 0 { items = []string{"No interrupted worker changes to recover"}; values = []string{""} }
	u.overlay = &overlay{
		title: "Recover worker changes",
		items: items,
		values: values,
		kind: "teamwork-recoveries",
		footer: "Enter validate and integrate · Esc back",
		parent: parent,
	}
	if u.lineMode { u.clearPrompt(); u.renderLineOverlay() } else { u.render() }
}

func (u *UI) selectTeamworkRecovery(index int) {
	if u.overlay == nil || index < 0 || index >= len(u.overlay.values) || u.overlay.values[index] == "" { return }
	id := u.overlay.values[index]
	if err := u.agent.RecoverTeamworkWorker(id); err != nil {
		u.notifyMessage("error", err.Error())
		return
	}
	u.notifyMessage("success", "Worker snapshot validated and integrated: "+id)
	u.openTeamworkRecoveries()
}

func teamworkModeFromEdit(key string) string {
	return strings.TrimPrefix(key, "teamwork:")
}
