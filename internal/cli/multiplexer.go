package cli

import (
	"github.com/joch/ygg/internal/multiplexer"
	"github.com/joch/ygg/internal/shell"
)

type shellSpawner func(dir, worktreeName string) error

func enterWorktree(dir, repoName, worktreeName string) error {
	return enterWorktreeWithSpawner(dir, repoName, worktreeName, multiplexer.Detect(), shell.Spawn)
}

func enterWorktreeWithSpawner(
	dir, repoName, worktreeName string,
	backend multiplexer.Backend,
	spawn shellSpawner,
) error {
	if backend != nil {
		info("Opening %s workspace...", backend.Name())
		if err := backend.Open(dir, repoName, worktreeName); err == nil {
			return nil
		} else {
			info("%s failed, falling back to subshell: %v", backend.Name(), err)
		}
	}

	info("Entering %s (exit to return)...", worktreeName)
	return spawn(dir, worktreeName)
}

func closeWorkspace(backend multiplexer.Backend, repoName, worktreeName string) error {
	if backend == nil {
		return nil
	}
	return backend.Close(repoName, worktreeName)
}
