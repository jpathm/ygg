package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/joch/ygg/internal/multiplexer"
	"github.com/joch/ygg/internal/worktree"
	"github.com/spf13/cobra"
)

var (
	cleanForce bool
	cleanDry   bool
)

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Remove worktrees with merged branches",
	Long: `Remove worktrees whose branches have been merged to the default branch.

By default, prompts for confirmation before removing each worktree.
Use --force to skip confirmation.
Use --dry-run to see what would be removed without actually removing.`,
	RunE: runClean,
}

func init() {
	rootCmd.AddCommand(cleanCmd)
	cleanCmd.Flags().BoolVarP(&cleanForce, "force", "f", false, "Skip confirmation prompts")
	cleanCmd.Flags().BoolVarP(&cleanDry, "dry-run", "n", false, "Show what would be removed without removing")
}

// selectMergedWorktrees returns the worktrees eligible for cleanup: non-primary
// worktrees whose branch is in the merged set. The default branch is never a
// candidate (MergedBranches no longer self-filters it now that it measures
// against a fully-qualified ref).
func selectMergedWorktrees(worktrees []*worktree.Worktree, mergedBranches []string, defaultBranch string) []*worktree.Worktree {
	mergedSet := make(map[string]bool, len(mergedBranches))
	for _, b := range mergedBranches {
		mergedSet[b] = true
	}

	var toRemove []*worktree.Worktree
	for _, wt := range worktrees {
		if wt.IsPrimary || wt.Branch == defaultBranch {
			continue
		}
		if mergedSet[wt.Branch] {
			toRemove = append(toRemove, wt)
		}
	}
	return toRemove
}

func runClean(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	wm, err := worktree.NewManager(cwd)
	if err != nil {
		errorMsg("Not in a git repository")
		return err
	}

	worktrees, err := wm.List()
	if err != nil {
		errorMsg("Failed to list worktrees: %v", err)
		return err
	}

	defaultBranch, err := wm.DefaultBranch()
	if err != nil {
		errorMsg("Could not detect default branch: %v", err)
		return err
	}

	// Find merged branches, measured against the resolved default tip
	// (origin/<default> when local is stale) so branches merged upstream are
	// detected consistently with how `ygg new` bases new worktrees.
	mergedBranches, err := wm.MergedBranches(wm.BaseRef(defaultBranch))
	if err != nil {
		errorMsg("Failed to get merged branches: %v", err)
		return err
	}

	toRemove := selectMergedWorktrees(worktrees, mergedBranches, defaultBranch)

	if len(toRemove) == 0 {
		info("No merged worktrees to clean up")
		return nil
	}

	info("Found %d merged worktree(s):", len(toRemove))
	for _, wt := range toRemove {
		fmt.Printf("  %s (branch: %s)\n", wt.Name, wt.Branch)
	}

	if cleanDry {
		info("Dry run - no worktrees removed")
		return nil
	}

	if !cleanForce {
		fmt.Print("\nRemove these worktrees? [y/N] ")
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			info("Aborted")
			return nil
		}
	}

	backend := multiplexer.Detect()
	for _, wt := range toRemove {
		result := removeWithWorkspace(
			backend,
			targetFor(wt, wm.RepoName()),
			func() error { return wm.Remove(wt.Name) },
		)
		if result.PrepareError != nil {
			info("Could not prepare workspace close for %s: %v", wt.Name, result.PrepareError)
		}
		if result.RemoveError != nil {
			errorMsg("Failed to remove %s: %v", wt.Name, result.RemoveError)
			continue
		}
		success("Removed %s", wt.Name)
		if result.CloseError != nil {
			info("Could not close workspace for %s: %v", wt.Name, result.CloseError)
		}
	}

	return nil
}
