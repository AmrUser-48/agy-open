package tui

import "context"

func (u *UI) openQuickActions() {
	u.overlay = &overlay{
		title: "Quick actions",
		items: []string{
			"Agent skills · discover and enable reusable workflows",
			"Team workspace · plan, coordinate, and recover work",
			"Scheduled tasks · create and manage reminders or automation",
			"Models · choose the active provider model",
			"Agent roles · choose a specialist role",
			"Settings · configure AGY",
			"Permissions · choose the tool approval mode",
			"Working-tree diff · review file changes",
			"System prompts · choose a prompt profile",
			"Help · keyboard controls and feature guide",
		},
		values: []string{"skills", "teamwork", "schedule", "models", "agents", "settings", "permissions", "diff", "prompts", "help"},
		kind: "quick-actions",
		footer: "Enter open · type / to filter · Alt+P close",
	}
	if u.lineMode { u.clearPrompt(); u.renderLineOverlay() } else { u.render() }
}

func (u *UI) selectQuickAction(index int, ctx context.Context) {
	if u.overlay == nil || index < 0 || index >= len(u.overlay.values) { return }
	action := u.overlay.values[index]
	u.overlay = nil
	switch action {
	case "skills":
		u.openSkills()
	case "teamwork":
		u.openTeamworkHome()
	case "schedule":
		u.openSchedulerHome()
	case "models":
		u.fetchModels(ctx)
	case "agents":
		u.fetchAgents()
	case "settings":
		u.openSettings()
		if u.lineMode { u.clearPrompt(); u.renderLineOverlay() }
	case "permissions":
		u.overlay = &overlay{
			title: "Permissions",
			items: append([]string(nil), permissionModeNames...),
			kind: "permissions",
			footer: "Enter apply · Esc close",
			index: permissionIndex(u.agent.ApprovalMode()),
		}
		if u.lineMode { u.clearPrompt(); u.renderLineOverlay() } else { u.render() }
	case "diff":
		u.showDiff()
	case "prompts":
		u.fetchSystemPrompts()
	case "help":
		u.openHelp()
	}
}
