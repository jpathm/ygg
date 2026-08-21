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
2. Resolve <name> against Linear. A name that looks like a Linear branch or
   identifier (for example snk-31-add-widget) is verified and used as typed.
   Any other name creates a Linear issue in the team mapped to this
   repository, and the worktree takes that issue's branch name.
3. Create a new worktree with a branch named <name>, based on the latest
   origin/<default-branch> (falling back to the local default branch when
   there is no remote)
4. Open the worktree in the active Herdr/tmux/Zellij workspace manager, or
   enter a subshell when no workspace backend is active

Linear is optional and never blocks. Without LINEAR_API_KEY, without a team
mapped in ~/.config/ygg/config.json, or when Linear cannot be reached, ygg
prints a warning and creates an unlinked worktree.

Exit the subshell to return to your original directory when ygg is not using
a workspace.`,
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

	// Resolve the requested name against Linear before creating the branch.
	// This can rename the worktree, so it must run before wm.Create.
	name = resolveTicket(cmd.Context(), wm, name)

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
