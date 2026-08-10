package multiplexer

import (
	"github.com/joch/ygg/internal/tmux"
	"github.com/joch/ygg/internal/zellij"
)

// Backend manages worktree workspaces in one terminal multiplexer.
type Target struct {
	Path         string
	RepoName     string
	WorktreeName string
	Branch       string
}

type Backend interface {
	Name() string
	Active() bool
	Open(Target) error
	Close(repoName, worktreeName string) error
}

type functionBackend struct {
	name     string
	activeFn func() bool
	openFn   func(Target) error
	closeFn  func(string, string) error
}

func (b functionBackend) Name() string {
	return b.name
}

func (b functionBackend) Active() bool {
	return b.activeFn()
}

func (b functionBackend) Open(target Target) error {
	return b.openFn(target)
}

func (b functionBackend) Close(repoName, worktreeName string) error {
	return b.closeFn(repoName, worktreeName)
}

func builtInBackends() []Backend {
	return []Backend{
		functionBackend{
			name:     "tmux",
			activeFn: tmux.InTmux,
			openFn: func(target Target) error {
				return tmux.OpenWindow(target.Path, target.RepoName, target.WorktreeName)
			},
			closeFn: tmux.CloseWindow,
		},
		functionBackend{
			name:     "zellij",
			activeFn: zellij.InZellij,
			openFn: func(target Target) error {
				return zellij.OpenTab(target.Path, target.RepoName, target.WorktreeName)
			},
			closeFn: zellij.CloseTab,
		},
	}
}

// Detect returns the highest-priority active backend, or nil.
func Detect() Backend {
	for _, backend := range builtInBackends() {
		if backend.Active() {
			return backend
		}
	}
	return nil
}
