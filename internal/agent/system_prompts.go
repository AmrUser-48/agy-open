package agent

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

//go:embed prompts/default.md prompts/t800.md
var embeddedSystemPromptProfiles embed.FS

var systemPromptProfileFilenamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}\.md$`)

func systemPromptProfilesDirectory() (string, error) {
	if configHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); configHome != "" {
		return filepath.Join(configHome, "agy-open", "sys-prompt"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory for system prompts: %w", err)
	}
	return filepath.Join(home, ".config", "agy-open", "sys-prompt"), nil
}

func normalizedSystemPromptFilename(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("system prompt profile name is required")
	}
	if !strings.HasSuffix(name, ".md") {
		name += ".md"
	}
	if filepath.Base(name) != name || !systemPromptProfileFilenamePattern.MatchString(name) {
		return "", fmt.Errorf("invalid system prompt profile %q; use a filename such as default.md", name)
	}
	return name, nil
}

// ensureSystemPromptDefaults copies embedded starter profiles only when their
// destination files do not exist. Existing user files are never overwritten.
func ensureSystemPromptDefaults() error {
	dir, err := systemPromptProfilesDirectory()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create system prompt directory %s: %w", dir, err)
	}
	for _, name := range []string{"default.md", "t800.md"} {
		data, err := embeddedSystemPromptProfiles.ReadFile(filepath.ToSlash(filepath.Join("prompts", name)))
		if err != nil {
			return fmt.Errorf("read embedded system prompt %s: %w", name, err)
		}
		path := filepath.Join(dir, name)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("create system prompt profile %s: %w", path, err)
		}
		_, writeErr := file.Write(data)
		closeErr := file.Close()
		if writeErr != nil {
			_ = os.Remove(path)
			return fmt.Errorf("write system prompt profile %s: %w", path, writeErr)
		}
		if closeErr != nil {
			_ = os.Remove(path)
			return fmt.Errorf("close system prompt profile %s: %w", path, closeErr)
		}
	}
	return nil
}

// ListSystemPromptProfiles returns available Markdown profile filenames.
func ListSystemPromptProfiles() ([]string, error) {
	if err := ensureSystemPromptDefaults(); err != nil {
		return nil, err
	}
	dir, err := systemPromptProfilesDirectory()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("list system prompt profiles: %w", err)
	}
	profiles := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !systemPromptProfileFilenamePattern.MatchString(name) || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		profiles = append(profiles, name)
	}
	sort.Slice(profiles, func(i, j int) bool {
		if profiles[i] == "default.md" {
			return profiles[j] != "default.md"
		}
		if profiles[j] == "default.md" {
			return false
		}
		return strings.ToLower(profiles[i]) < strings.ToLower(profiles[j])
	})
	return profiles, nil
}

func loadSystemPromptProfile(name string) (string, string, error) {
	filename, err := normalizedSystemPromptFilename(name)
	if err != nil {
		return "", "", err
	}
	if err := ensureSystemPromptDefaults(); err != nil {
		return "", "", err
	}
	dir, err := systemPromptProfilesDirectory()
	if err != nil {
		return "", "", err
	}
	path := filepath.Join(dir, filename)
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", fmt.Errorf("system prompt profile %q not found; use /system-prompt to list profiles", filename)
		}
		return "", "", fmt.Errorf("inspect system prompt profile %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("system prompt profile %q is not a regular file", filename)
	}
	if info.Size() > maxCustomSystemPromptBytes {
		return "", "", fmt.Errorf("system prompt profile %q exceeds the %d-byte limit", filename, maxCustomSystemPromptBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("read system prompt profile %s: %w", path, err)
	}
	if len(data) > maxCustomSystemPromptBytes {
		return "", "", fmt.Errorf("system prompt profile %q exceeds the %d-byte limit", filename, maxCustomSystemPromptBytes)
	}
	return string(data), filename, nil
}

// SetSystemPromptProfile loads a Markdown profile and persists it as the active
// custom system instruction. The mandatory Agy operating protocol is still added
// separately when each request is composed.
func (a *Agent) SetSystemPromptProfile(name string) error {
	prompt, filename, err := loadSystemPromptProfile(name)
	if err != nil {
		return err
	}
	previousPrompt := a.cfg.SystemPrompt
	previousProfile := a.cfg.SystemPromptProfile
	a.cfg.SystemPrompt = prompt
	a.cfg.SystemPromptProfile = filename
	if err := a.SaveConfig(); err != nil {
		a.cfg.SystemPrompt = previousPrompt
		a.cfg.SystemPromptProfile = previousProfile
		return err
	}
	return nil
}
