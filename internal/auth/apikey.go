package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const maxAPIKeyBytes = 64 * 1024

// APIKeyStore keeps non-OAuth provider keys in a file separate from settings
// and OAuth token stores. The file and its parent directory are owner-only.
type APIKeyStore struct {
	Path string
	mu   sync.Mutex
}

// APIKeyStatus never includes the credential value.
type APIKeyStatus struct {
	Provider    string
	Source      string // "environment", "saved", or "missing"
	Environment string
}

var apiKeyEnvironment = map[string][]string{
	"gemini-api":       {"AGY_GEMINI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY"},
	"ai-studio":        {"AGY_AI_STUDIO_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY"},
	"gemini-live":      {"AGY_GEMINI_LIVE_API_KEY", "GEMINI_LIVE_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY"},
	"openai-api":       {"AGY_OPENAI_API_KEY", "OPENAI_API_KEY"},
	"openai-compatible":{"AGY_LOCAL_API_KEY"},
	"openrouter":       {"AGY_OPENROUTER_API_KEY", "OPENROUTER_API_KEY"},
}

func NewAPIKeyStore() *APIKeyStore {
	return &APIKeyStore{}
}

func IsAPIKeyProvider(provider string) bool {
	_, ok := apiKeyEnvironment[provider]
	return ok
}

// APIKeyEnvironmentNames returns the ordered environment-variable names for a
// provider. Environment variables are consulted only after that provider has
// explicitly been selected.
func APIKeyEnvironmentNames(provider string) []string {
	names := apiKeyEnvironment[provider]
	return append([]string(nil), names...)
}

func (s *APIKeyStore) path() (string, error) {
	if s != nil && strings.TrimSpace(s.Path) != "" {
		return s.Path, nil
	}
	root := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("locate user configuration directory: %w", err)
		}
		root = filepath.Join(home, ".config")
	}
	return filepath.Join(root, "agy-open", "api-keys.json"), nil
}

func (s *APIKeyStore) readLocked() (map[string]string, string, error) {
	path, err := s.path()
	if err != nil {
		return nil, "", err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, path, nil
	}
	if err != nil {
		return nil, path, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, path, errors.New("API-key store must be a regular, non-symlink file")
	}
	if info.Mode().Perm()&0077 != 0 {
		if err := os.Chmod(path, 0600); err != nil {
			return nil, path, fmt.Errorf("secure API-key store permissions: %w", err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, path, err
	}
	var values map[string]string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, path, fmt.Errorf("decode API-key store: %w", err)
	}
	if values == nil {
		values = map[string]string{}
	}
	return values, path, nil
}

func (s *APIKeyStore) Get(provider string) (string, error) {
	if !IsAPIKeyProvider(provider) {
		return "", fmt.Errorf("provider %q does not use a stored API key", provider)
	}
	if s == nil {
		s = NewAPIKeyStore()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	values, _, err := s.readLocked()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(values[provider]), nil
}

func (s *APIKeyStore) Set(provider, key string) error {
	if !IsAPIKeyProvider(provider) {
		return fmt.Errorf("provider %q does not accept an API key", provider)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("API key for %s is empty", provider)
	}
	if len(key) > maxAPIKeyBytes {
		return fmt.Errorf("API key input is too large")
	}
	if strings.ContainsAny(key, "\r\n\x00") {
		return errors.New("API key must be a single line")
	}
	if s == nil {
		s = NewAPIKeyStore()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	values, path, err := s.readLocked()
	if err != nil {
		return err
	}
	values[provider] = key
	return s.writeLocked(path, values)
}

func (s *APIKeyStore) SetFromReader(provider string, input io.Reader) error {
	if input == nil {
		return errors.New("read API key from stdin")
	}
	raw, err := io.ReadAll(io.LimitReader(input, maxAPIKeyBytes+1))
	if err != nil {
		return fmt.Errorf("read API key: %w", err)
	}
	if len(raw) > maxAPIKeyBytes {
		return errors.New("API key input is too large")
	}
	return s.Set(provider, string(raw))
}

func (s *APIKeyStore) Remove(provider string) error {
	if !IsAPIKeyProvider(provider) {
		return fmt.Errorf("provider %q does not use a stored API key", provider)
	}
	if s == nil {
		s = NewAPIKeyStore()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	values, path, err := s.readLocked()
	if err != nil {
		return err
	}
	delete(values, provider)
	if len(values) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return s.writeLocked(path, values)
}

func (s *APIKeyStore) writeLocked(path string, values map[string]string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create API-key store directory: %w", err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return fmt.Errorf("secure API-key store directory: %w", err)
	}
	raw, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".api-keys-*")
	if err != nil {
		return fmt.Errorf("create API-key store temporary file: %w", err)
	}
	tmp := file.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(append(raw, '\n')); err != nil {
		_ = file.Close()
		return fmt.Errorf("write API-key store: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync API-key store: %w", err)
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace API-key store: %w", err)
	}
	ok = true
	return nil
}

// Status reports whether an effective key is available without returning it.
func (s *APIKeyStore) Status(provider string) (APIKeyStatus, error) {
	status := APIKeyStatus{Provider: provider, Source: "missing"}
	names, ok := apiKeyEnvironment[provider]
	if !ok {
		return status, fmt.Errorf("provider %q does not use an API key", provider)
	}
	for _, name := range names {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			status.Source = "environment"
			status.Environment = name
			return status, nil
		}
	}
	key, err := s.Get(provider)
	if err != nil {
		return status, err
	}
	if key != "" {
		status.Source = "saved"
	}
	return status, nil
}

// ResolveAPIKey applies environment-over-store precedence for the explicitly
// selected API-key provider. OAuth providers never consume ambient API keys.
func ResolveAPIKey(provider string, store *APIKeyStore) (string, error) {
	if provider == "gemini" || provider == "openai" {
		return "", nil
	}
	names, ok := apiKeyEnvironment[provider]
	if !ok {
		return "", fmt.Errorf("unsupported model provider %q", provider)
	}
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value, nil
		}
	}
	return store.Get(provider)
}
