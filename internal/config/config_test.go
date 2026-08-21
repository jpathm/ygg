package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeConfig points os.UserConfigDir at a temp dir and writes body to
// ygg/config.json inside it. Passing an empty body writes no file at all.
func writeConfig(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if body == "" {
		return
	}
	if err := os.MkdirAll(filepath.Join(dir, "ygg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ygg", "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	writeConfig(t, "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if cfg.Linear.DefaultTeam != "" || len(cfg.Linear.Teams) != 0 {
		t.Errorf("Load() = %+v, want zero Config", cfg)
	}
}

func TestLoadParsesConfig(t *testing.T) {
	writeConfig(t, `{"linear":{"defaultTeam":"SKUNK","teams":{"GridKitLLC/ygg":"SKUNK"}}}`)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Linear.DefaultTeam != "SKUNK" {
		t.Errorf("DefaultTeam = %q, want SKUNK", cfg.Linear.DefaultTeam)
	}
	if cfg.Linear.Teams["GridKitLLC/ygg"] != "SKUNK" {
		t.Errorf("Teams = %v", cfg.Linear.Teams)
	}
}

func TestLoadMalformedFileIsAnError(t *testing.T) {
	writeConfig(t, `{"linear":`)
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want a parse error")
	}
}

func TestNormalizeRemote(t *testing.T) {
	tests := []struct {
		remote string
		want   string
	}{
		{"git@github.com:GridKitLLC/ygg.git", "GridKitLLC/ygg"},
		{"git@github.com:GridKitLLC/ygg", "GridKitLLC/ygg"},
		{"https://github.com/GridKitLLC/ygg.git", "GridKitLLC/ygg"},
		{"https://github.com/GridKitLLC/ygg", "GridKitLLC/ygg"},
		{"ssh://git@github.com/GridKitLLC/ygg.git", "GridKitLLC/ygg"},
		{"https://user@github.com/GridKitLLC/ygg", "GridKitLLC/ygg"},
		{"  https://github.com/GridKitLLC/ygg  ", "GridKitLLC/ygg"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := NormalizeRemote(tt.remote); got != tt.want {
			t.Errorf("NormalizeRemote(%q) = %q, want %q", tt.remote, got, tt.want)
		}
	}
}

func TestTeamFor(t *testing.T) {
	cfg := Config{Linear: Linear{
		DefaultTeam: "HELIUM",
		Teams:       map[string]string{"GridKitLLC/ygg": "SKUNK"},
	}}

	tests := []struct {
		name   string
		cfg    Config
		remote string
		want   string
	}{
		{"exact match wins", cfg, "git@github.com:GridKitLLC/ygg.git", "SKUNK"},
		{"unmapped falls back to default", cfg, "git@github.com:other/thing.git", "HELIUM"},
		{"no default and no match yields empty", Config{}, "git@github.com:other/thing.git", ""},
		{"empty remote falls back to default", cfg, "", "HELIUM"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.TeamFor(tt.remote); got != tt.want {
				t.Errorf("TeamFor(%q) = %q, want %q", tt.remote, got, tt.want)
			}
		})
	}
}
