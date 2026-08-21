// Package config loads ygg's optional user configuration. ygg works without a
// config file; the file only enables features that need per-repository
// settings, such as Linear team routing.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Linear holds Linear-specific settings. It never holds credentials — the API
// key comes from the LINEAR_API_KEY environment variable so that a secret is
// not written to a file that may be synchronised between machines.
type Linear struct {
	// DefaultTeam is the team key used when no per-repository entry matches.
	DefaultTeam string `json:"defaultTeam"`
	// Teams maps a normalized "owner/repo" remote onto a Linear team key.
	Teams map[string]string `json:"teams"`
}

// Config is ygg's user configuration.
type Config struct {
	Linear Linear `json:"linear"`
}

// Path returns the location of ygg's config file. It honors XDG_CONFIG_HOME.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("could not locate user config dir: %w", err)
	}
	return filepath.Join(dir, "ygg", "config.json"), nil
}

// Load reads the config file. A missing file yields a zero Config and no
// error, so installing a config-dependent feature never breaks ygg on a
// machine that has not been configured. A malformed file is an error, because
// silently ignoring a typo would leave the user believing a feature is active
// when it is not.
func Load() (Config, error) {
	var cfg Config

	path, err := Path()
	if err != nil {
		return cfg, err
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("could not read %s: %w", path, err)
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("could not parse %s: %w", path, err)
	}
	return cfg, nil
}

// NormalizeRemote reduces a git remote URL to "owner/repo", so that SSH and
// HTTPS remotes for the same repository produce the same key.
func NormalizeRemote(remote string) string {
	s := strings.TrimSpace(remote)
	s = strings.TrimSuffix(s, ".git")

	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
		// Drop everything up to the first slash: userinfo and host.
		if j := strings.Index(s, "/"); j >= 0 {
			s = s[j+1:]
		}
	} else if i := strings.Index(s, ":"); i >= 0 && strings.Contains(s[:i], "@") {
		// scp-style: git@github.com:owner/repo
		s = s[i+1:]
	}

	return strings.Trim(s, "/")
}

// TeamFor returns the Linear team key for a remote URL, preferring an exact
// per-repository entry and falling back to DefaultTeam. It returns "" when
// neither is configured.
func (c Config) TeamFor(remote string) string {
	if team, ok := c.Linear.Teams[NormalizeRemote(remote)]; ok && team != "" {
		return team
	}
	return c.Linear.DefaultTeam
}
