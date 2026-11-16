package config

import (
	"fmt"
	"regexp"
	"strings"
)

type MCPServerConfig struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Disabled bool             `json:"disabled,omitempty"`
}

var mcpServerNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,39}$`)
var mcpEnvNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func ValidateMCPServers(servers []MCPServerConfig) error {
	if len(servers) > 64 {
		return fmt.Errorf("at most 64 MCP servers may be configured")
	}
	seen := make(map[string]bool, len(servers))
	for i, server := range servers {
		name := strings.TrimSpace(server.Name)
		if !mcpServerNamePattern.MatchString(name) {
			return fmt.Errorf("MCP server %d has invalid name %q (use 1–40 letters, digits, underscores, or hyphens)", i+1, server.Name)
		}
		if seen[strings.ToLower(name)] {
			return fmt.Errorf("duplicate MCP server name %q", name)
		}
		seen[strings.ToLower(name)] = true
		if strings.TrimSpace(server.Command) == "" || len(server.Command) > 4096 {
			return fmt.Errorf("MCP server %q requires a command of at most 4096 bytes", name)
		}
		if len(server.Args) > 128 {
			return fmt.Errorf("MCP server %q has more than 128 arguments", name)
		}
		for _, arg := range server.Args {
			if strings.ContainsRune(arg, 0) || len(arg) > 8192 {
				return fmt.Errorf("MCP server %q has an invalid or oversized argument", name)
			}
		}
		if len(server.Env) > 128 {
			return fmt.Errorf("MCP server %q has more than 128 environment overrides", name)
		}
		for key, value := range server.Env {
			if !mcpEnvNamePattern.MatchString(key) || strings.ContainsRune(value, 0) || len(value) > 8192 {
				return fmt.Errorf("MCP server %q has an invalid environment override %q", name, key)
			}
		}
	}
	return nil
}

func (cfg *Config) UpsertMCPServer(server MCPServerConfig) error {
	if cfg == nil {
		return fmt.Errorf("nil config")
	}
	servers := append([]MCPServerConfig(nil), cfg.MCPServers...)
	for i := range servers {
		if strings.EqualFold(servers[i].Name, server.Name) {
			servers[i] = server
			if err := ValidateMCPServers(servers); err != nil {
				return err
			}
			cfg.MCPServers = servers
			return nil
		}
	}
	servers = append(servers, server)
	if err := ValidateMCPServers(servers); err != nil {
		return err
	}
	cfg.MCPServers = servers
	return nil
}

func (cfg *Config) RemoveMCPServer(name string) error {
	if cfg == nil {
		return fmt.Errorf("nil config")
	}
	servers := make([]MCPServerConfig, 0, len(cfg.MCPServers))
	found := false
	for _, server := range cfg.MCPServers {
		if strings.EqualFold(server.Name, strings.TrimSpace(name)) {
			found = true
			continue
		}
		servers = append(servers, server)
	}
	if !found {
		return fmt.Errorf("MCP server %q is not configured", name)
	}
	cfg.MCPServers = servers
	return nil
}

func (cfg *Config) SetMCPServerDisabled(name string, disabled bool) error {
	if cfg == nil {
		return fmt.Errorf("nil config")
	}
	for i := range cfg.MCPServers {
		if strings.EqualFold(cfg.MCPServers[i].Name, strings.TrimSpace(name)) {
			cfg.MCPServers[i].Disabled = disabled
			return nil
		}
	}
	return fmt.Errorf("MCP server %q is not configured", name)
}
