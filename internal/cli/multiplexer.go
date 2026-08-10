package cli

import (
	"github.com/joch/ygg/internal/multiplexer"
	"github.com/joch/ygg/internal/shell"
	"github.com/joch/ygg/internal/worktree"
)

type shellSpawner func(dir, worktreeName string) error

func targetFor(wt *worktree.Worktree, repoName string) multiplexer.Target {
	return multiplexer.Target{
		Path: wt.Path, RepoName: repoName,
		WorktreeName: wt.Name, Branch: wt.Branch,
	}
}

func enterWorktree(target multiplexer.Target) error {
	return enterWorktreeWithSpawner(target, multiplexer.Detect(), shell.Spawn)
}

func enterWorktreeWithSpawner(
	target multiplexer.Target,
	backend multiplexer.Backend,
	spawn shellSpawner,
) error {
	if backend != nil {
		info("Opening %s workspace...", backend.Name())
		if err := backend.Open(target); err == nil {
			return nil
		} else {
			info("%s failed, falling back to subshell: %v", backend.Name(), err)
		}
	}

	info("Entering %s (exit to return)...", target.WorktreeName)
	return spawn(target.Path, target.WorktreeName)
}

func closeWorkspace(backend multiplexer.Backend, repoName, worktreeName string) error {
	if backend == nil {
		return nil
	}
	return backend.Close(repoName, worktreeName)
}
