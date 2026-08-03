package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const windowListFormat = "#{window_id}\t#{window_name}"

type commandRunner interface {
	Output(name string, args ...string) ([]byte, error)
	Run(name string, args ...string) error
}

type execRunner struct{}

func (execRunner) Output(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

func (execRunner) Run(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

var commands commandRunner = execRunner{}

// InTmux reports whether ygg is running inside a tmux client.
func InTmux() bool {
	return os.Getenv("TMUX") != ""
}

// WindowName returns the tmux window name for a worktree.
func WindowName(repoName, worktreeName string) string {
	return repoName + "/" + worktreeName
}

func findWindowID(name string) (string, bool, error) {
	output, err := commands.Output("tmux", "list-windows", "-F", windowListFormat)
	if err != nil {
		return "", false, fmt.Errorf("failed to list tmux windows for %q: %w", name, err)
	}

	var match string
	for _, line := range strings.Split(strings.TrimSuffix(string(output), "\n"), "\n") {
		if line == "" {
			continue
		}
		id, windowName, ok := strings.Cut(line, "\t")
		if !ok {
			return "", false, fmt.Errorf("unexpected tmux window record %q", line)
		}
		if windowName != name {
			continue
		}
		if match != "" {
			return "", false, fmt.Errorf("multiple tmux windows named %q", name)
		}
		match = id
	}

	return match, match != "", nil
}

// OpenWindow focuses an existing exact match or creates a tmux window in dir.
func OpenWindow(dir, repoName, worktreeName string) error {
	name := WindowName(repoName, worktreeName)
	id, found, err := findWindowID(name)
	if err != nil {
		return err
	}
	if found {
		if err := commands.Run("tmux", "select-window", "-t", id); err != nil {
			return fmt.Errorf("failed to select tmux window %q: %w", name, err)
		}
		return nil
	}

	if err := commands.Run("tmux", "new-window", "-n", name, "-c", dir); err != nil {
		return fmt.Errorf("failed to create tmux window %q: %w", name, err)
	}
	return nil
}

// CloseWindow closes a unique exact-matching tmux window, if one exists.
func CloseWindow(repoName, worktreeName string) error {
	name := WindowName(repoName, worktreeName)
	id, found, err := findWindowID(name)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	if err := commands.Run("tmux", "kill-window", "-t", id); err != nil {
		return fmt.Errorf("failed to close tmux window %q: %w", name, err)
	}
	return nil
}
