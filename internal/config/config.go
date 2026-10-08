package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Config struct {
	ModelProvider string `json:"modelProvider"`
	Model         string `json:"model"`
	MaxTurns      int    `json:"maxTurns"`
	ApprovalMode  string `json:"approvalMode"`
	Theme         string `json:"theme"`
	Effort        string `json:"effort"`
}

func defaults() Config {
	return Config{
		ModelProvider: "gemini",
		Model:         "gemini-3.8-flash",
		MaxTurns:      12,
		ApprovalMode:  "ask",
		Theme:         "default",
		Effort:        "medium",
	}
}

func path() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "agy", "settings.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "agy", "settings.json")
}

func Load() (Config, error) {
	cfg := defaults()
	b, err := os.ReadFile(path())
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return defaults(), nil
	}
	return cfg, nil
}
