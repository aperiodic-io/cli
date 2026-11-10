package aperiodic

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Credentials is what `aperiodic login` saves: a named key of this machine's
// own, revocable on its own at aperiodic.io/home/api. The Python client reads
// and writes the same file. Everything but api_key is a note for people, kept
// as text so that no other writer's format can make the key unreadable.
type Credentials struct {
	APIKey    string `json:"api_key"`
	KeyName   string `json:"key_name,omitempty"`
	Plan      string `json:"plan,omitempty"`
	PlanName  string `json:"plan_name,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

// CredentialsPath is where the key is kept: $APERIODIC_CONFIG_DIR, else
// $XDG_CONFIG_HOME/aperiodic, else %APPDATA%\aperiodic on Windows and
// ~/.config/aperiodic elsewhere, macOS included, so the CLI and the Python
// client agree on one place.
func CredentialsPath(env func(string) string) (string, error) {
	if dir := env("APERIODIC_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "credentials"), nil
	}
	if dir := env("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "aperiodic", "credentials"), nil
	}
	if runtime.GOOS == "windows" {
		if dir := env("APPDATA"); dir != "" {
			return filepath.Join(dir, "aperiodic", "credentials"), nil
		}
	}
	home := env("HOME")
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return "", fmt.Errorf("finding the home directory: %w", err)
		}
	}
	return filepath.Join(home, ".config", "aperiodic", "credentials"), nil
}

// LoadCredentials reads the saved key; nil, nil when there is none.
func LoadCredentials(env func(string) string) (*Credentials, error) {
	path, err := CredentialsPath(env)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if creds.APIKey == "" {
		return nil, nil
	}
	return &creds, nil
}

// SaveCredentials writes the key readable by this user only.
func SaveCredentials(env func(string) string, creds Credentials) (string, error) {
	path, err := CredentialsPath(env)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return "", err
	}
	// A fresh temporary file per write: two logins at once cannot share it,
	// and no stale file left behind lends it a looser mode.
	tmp, err := os.CreateTemp(filepath.Dir(path), "credentials-*")
	if err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		tmp.Close()
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return path, nil
}

// DeleteCredentials removes the saved key; a missing file is not an error.
func DeleteCredentials(env func(string) string) error {
	path, err := CredentialsPath(env)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing %s: %w", path, err)
	}
	return nil
}
