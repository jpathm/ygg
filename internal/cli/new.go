package cli

import (
	"fmt"
	"os"

	"github.com/joch/ygg/internal/worktree"
	"github.com/spf13/cobra"
)

var newCmd = &cobra.Command{
	Use:   "new <name>",
	Short: "Create a new worktree and enter it",
	Long: `Create a new git worktree with the specified name.

This will:
1. Fetch the latest changes from origin
2. Create a new worktree with a branch named <name>, based on the latest
   origin/<default-branch> (falling back to the local default branch when
   there is no remote)
3. Open the worktree in the active tmux/Zellij multiplexer, or enter a
   subshell when no multiplexer is active

Exit the subshell to return to your original directory when ygg is not using
a multiplexer workspace.`,
	Args: cobra.ExactArgs(1),
	RunE: runNew,
}

func init() {
	rootCmd.AddCommand(newCmd)
}

func runNew(cmd *cobra.Command, args []string) error {
	name := args[0]

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	wm, err := worktree.NewManager(cwd)
	if err != nil {
		errorMsg("Not in a git repository")
		return err
	}

	// Fetch latest
	info("Fetching from origin...")
	if err := wm.Fetch(); err != nil {
		// Non-fatal, continue anyway
		info("Could not fetch (offline?)")
	}

	// Detect the default branch (main/master); the worktree is based on the
	// freshly fetched origin/<default> tip, so no local pull is required.
	defaultBranch, err := wm.DefaultBranch()
	if err != nil {
		errorMsg("Could not detect default branch: %v", err)
		return err
	}

	info("Creating worktree: %s (default branch %s)", name, defaultBranch)

	wt, err := wm.Create(name)
	if err != nil {
		errorMsg("Failed to create worktree: %v", err)
		return err
	}

	if wt.Base != "" {
		success("Created worktree at %s (based on %s)", wt.Path, wt.Base)
	} else {
		success("Created worktree at %s", wt.Path)
	}

	// Warn if .worktrees is not in .gitignore
	if !wm.IsWorktreeDirIgnored() {
		info("Warning: .worktrees is not in .gitignore — consider adding it")
	}

	// Report on copied files
	if wt.CopyError != nil {
		info("Warning: failed to copy some files: %v", wt.CopyError)
	} else if wt.CopiedFiles > 0 {
		info("Copied %d untracked file(s) from main worktree", wt.CopiedFiles)
	}

	return enterWorktree(targetFor(wt, wm.RepoName()))
}
