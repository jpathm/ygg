package cli

import (
	"fmt"
	"os"

	"github.com/joch/ygg/internal/worktree"
	"github.com/spf13/cobra"
)

var switchCmd = &cobra.Command{
	Use:   "switch <name>",
	Short: "Switch to a worktree",
	Long: `Switch to an existing worktree by name.

Inside tmux or Zellij, this focuses an existing named workspace or creates one.
Otherwise, it spawns a subshell in the worktree directory; exit that shell to
return to your original directory.`,
	Args:              cobra.ExactArgs(1),
	Aliases:           []string{"sw"},
	RunE:              runSwitch,
	ValidArgsFunction: completeWorktreeNames,
}

func init() {
	rootCmd.AddCommand(switchCmd)
}

func runSwitch(cmd *cobra.Command, args []string) error {
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

	wt, err := wm.Get(name)
	if err != nil {
		errorMsg("Worktree %q not found", name)
		return err
	}

	// If already in a ygg shell, just output cd command for the wrapper to eval
	if InYggShell() {
		fmt.Printf("cd %s\n", wt.Path)
		return nil
	}

	return enterWorktree(wt.Path, wm.RepoName(), wt.Name)
}
