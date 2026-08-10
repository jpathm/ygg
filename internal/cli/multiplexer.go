package cli

import (
	"fmt"
	"os"

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
	backend := multiplexer.Detect()
	if useYggShell(backend) {
		fmt.Printf("cd %s\n", target.Path)
		return nil
	}
	return enterWorktreeWithSpawner(target, backend, shell.Spawn)
}

func useYggShell(backend multiplexer.Backend) bool {
	return InYggShell() && (backend == nil || backend.Name() != "Herdr")
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

type workspaceRemovalResult struct {
	WorkspaceHandled bool
	PrepareError     error
	RemoveError      error
	CloseError       error
}

func removeWithWorkspace(
	backend multiplexer.Backend,
	target multiplexer.Target,
	remove func() error,
) workspaceRemovalResult {
	var result workspaceRemovalResult
	var plan multiplexer.ClosePlan
	if backend != nil {
		plan, result.PrepareError = backend.PrepareClose(target)
	}
	result.RemoveError = remove()
	if result.RemoveError != nil || result.PrepareError != nil {
		return result
	}
	result.CloseError = plan.Execute()
	result.WorkspaceHandled = result.CloseError == nil && plan.Matched()
	return result
}

func returnToPrimary(mainPath string) error {
	// Change directory before starting fallback navigation because the caller's
	// worktree no longer exists.
	if err := os.Chdir(mainPath); err != nil {
		return fmt.Errorf("failed to change to main repo: %w", err)
	}
	if InYggShell() {
		fmt.Printf("cd %s\n", mainPath)
		return nil
	}
	info("Returning to main...")
	return shell.Spawn(mainPath, "main")
}
