package tui

import "fmt"

func (u *UI) openSkills() {
	skills := u.agent.RefreshSkills()
	active := make(map[string]bool)
	for _, name := range u.agent.ActiveSkillNames() { active[name] = true }
	items := make([]string, 0, len(skills))
	values := make([]string, 0, len(skills))
	for _, skill := range skills {
		mark := "○ "
		if active[skill.Name] { mark = "● " }
		scope := "user"
		if skill.Source == "project" { scope = "project" }
		items = append(items, mark+skill.Name+" · "+skill.Description+" ["+scope+"]")
		values = append(values, skill.Name)
	}
	if len(items) == 0 {
		items = []string{"No skills found"}
		values = []string{""}
	}
	u.overlay = &overlay{
		title: "Agent skills",
		items: items,
		values: values,
		kind: "skills",
		footer: "Enter toggle · ● enabled for future requests · Esc close",
	}
	if u.lineMode {
		u.clearPrompt()
		u.renderLineOverlay()
	} else {
		u.render()
	}
}

func (u *UI) selectSkill(index int) {
	if u.overlay == nil || index < 0 || index >= len(u.overlay.values) || u.overlay.values[index] == "" {
		return
	}
	name := u.overlay.values[index]
	enabled, err := u.agent.ToggleSkill(name)
	if err != nil {
		u.notifyMessage("error", err.Error())
		return
	}
	if enabled {
		u.notifyMessage("success", fmt.Sprintf("Skill %q enabled for future requests", name))
	} else {
		u.notifyMessage("info", fmt.Sprintf("Skill %q disabled", name))
	}
	u.openSkills()
}
