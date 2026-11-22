package agent

import (
	"fmt"
	"strings"

	"github.com/AmrUser-48/agy-open/internal/skills"
)

// AvailableSkills returns Agent Skills-compatible skills visible to this workspace.
func (a *Agent) AvailableSkills() []skills.Skill {
	if a == nil || a.tools == nil { return nil }
	return skills.Discover(a.tools.Root)
}

// ActiveSkillNames returns the skills explicitly enabled for future requests.
func (a *Agent) ActiveSkillNames() []string {
	if a == nil { return nil }
	a.skillsMu.RLock()
	defer a.skillsMu.RUnlock()
	return append([]string(nil), a.activeSkills...)
}

// ToggleSkill enables or disables a discovered skill by name.
func (a *Agent) ToggleSkill(name string) (bool, error) {
	available := a.AvailableSkills()
	skill, ok := skills.ReadSkill(available, name)
	if !ok { return false, fmt.Errorf("skill %q was not found; refresh the skills list", name) }
	a.skillsMu.Lock()
	defer a.skillsMu.Unlock()
	for i, active := range a.activeSkills {
		if strings.EqualFold(active, skill.Name) {
			a.activeSkills = append(a.activeSkills[:i], a.activeSkills[i+1:]...)
			return false, nil
		}
	}
	a.activeSkills = append(a.activeSkills, skill.Name)
	return true, nil
}

func (a *Agent) skillContext(prompt string) string {
	if a == nil || a.tools == nil { return "" }
	a.skillsMu.RLock()
	active := append([]string(nil), a.activeSkills...)
	a.skillsMu.RUnlock()
	return skills.BuildContext(skills.Discover(a.tools.Root), prompt, active)
}
