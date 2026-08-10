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

// ClosePlan captures a workspace close operation prepared before its
// associated worktree is removed.
type ClosePlan struct {
	matched bool
	execute func() error
}

// NewClosePlan creates a workspace close plan.
func NewClosePlan(matched bool, execute func() error) ClosePlan {
	return ClosePlan{matched: matched, execute: execute}
}

// Matched reports whether preparation found a workspace that can handle the
// caller's return-to-primary behavior.
func (p ClosePlan) Matched() bool {
	return p.matched
}

// Execute runs the prepared close operation. The zero plan is a no-op.
func (p ClosePlan) Execute() error {
	if p.execute == nil {
		return nil
	}
	return p.execute()
}

type Backend interface {
	Name() string
	Active() bool
	Open(Target) error
	PrepareClose(Target) (ClosePlan, error)
}

type functionBackend struct {
	name           string
	activeFn       func() bool
	openFn         func(Target) error
	prepareCloseFn func(Target) (ClosePlan, error)
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

func (b functionBackend) PrepareClose(target Target) (ClosePlan, error) {
	return b.prepareCloseFn(target)
}

func builtInBackends() []Backend {
	return []Backend{
		functionBackend{
			name:     "tmux",
			activeFn: tmux.InTmux,
			openFn: func(target Target) error {
				return tmux.OpenWindow(target.Path, target.RepoName, target.WorktreeName)
			},
			prepareCloseFn: func(target Target) (ClosePlan, error) {
				return NewClosePlan(false, func() error {
					return tmux.CloseWindow(target.RepoName, target.WorktreeName)
				}), nil
			},
		},
		functionBackend{
			name:     "zellij",
			activeFn: zellij.InZellij,
			openFn: func(target Target) error {
				return zellij.OpenTab(target.Path, target.RepoName, target.WorktreeName)
			},
			prepareCloseFn: func(target Target) (ClosePlan, error) {
				return NewClosePlan(false, func() error {
					return zellij.CloseTab(target.RepoName, target.WorktreeName)
				}), nil
			},
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
