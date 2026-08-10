package herdr

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type commandRunner interface {
	CombinedOutput(name string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) CombinedOutput(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

var commands commandRunner = execRunner{}

func InHerdr() bool { return os.Getenv("HERDR_ENV") == "1" }

func WorkspaceLabel(branch, worktreeName string) string {
	if branch != "" {
		return branch
	}
	return worktreeName
}

type responseEnvelope struct {
	Result responseResult `json:"result"`
}

type responseResult struct {
	Type       string          `json:"type"`
	Workspace  workspaceInfo   `json:"workspace"`
	Workspaces []workspaceInfo `json:"workspaces"`
}

type workspaceInfo struct {
	WorkspaceID string             `json:"workspace_id"`
	Worktree    *workspaceWorktree `json:"worktree"`
}

type workspaceWorktree struct {
	CheckoutPath string `json:"checkout_path"`
}

func OpenWorkspace(path, branch, worktreeName string) error {
	workspaceID := os.Getenv("HERDR_WORKSPACE_ID")
	if workspaceID == "" {
		return fmt.Errorf("cannot open Herdr workspace for %q: HERDR_WORKSPACE_ID is not set", path)
	}
	output, err := commands.CombinedOutput(
		"herdr", "worktree", "open",
		"--workspace", workspaceID,
		"--path", path,
		"--label", WorkspaceLabel(branch, worktreeName),
		"--focus",
	)
	if err != nil {
		return fmt.Errorf("failed to open Herdr workspace for %q: %w: %s",
			path, err, strings.TrimSpace(string(output)))
	}
	var response responseEnvelope
	if err := json.Unmarshal(output, &response); err != nil {
		return fmt.Errorf("failed to parse Herdr worktree open response for %q: %w", path, err)
	}
	if response.Result.Type != "worktree_opened" {
		return fmt.Errorf("unexpected Herdr worktree open response type %q for %q", response.Result.Type, path)
	}
	if response.Result.Workspace.WorkspaceID == "" {
		return fmt.Errorf("Herdr worktree open response for %q has no workspace ID", path)
	}
	return nil
}

func PrepareClose(path string) (string, bool, error) {
	targetPath, err := filepath.Abs(path)
	if err != nil {
		return "", false, fmt.Errorf("failed to resolve Herdr checkout path %q: %w", path, err)
	}
	targetPath = filepath.Clean(targetPath)
	output, err := commands.CombinedOutput("herdr", "workspace", "list")
	if err != nil {
		return "", false, fmt.Errorf("failed to list Herdr workspaces for %q: %w: %s",
			targetPath, err, strings.TrimSpace(string(output)))
	}
	var response responseEnvelope
	if err := json.Unmarshal(output, &response); err != nil {
		return "", false, fmt.Errorf("failed to parse Herdr workspace list for %q: %w", targetPath, err)
	}
	if response.Result.Type != "workspace_list" {
		return "", false, fmt.Errorf("unexpected Herdr workspace list response type %q", response.Result.Type)
	}
	var match string
	for _, workspace := range response.Result.Workspaces {
		if workspace.Worktree == nil ||
			filepath.Clean(workspace.Worktree.CheckoutPath) != targetPath {
			continue
		}
		if workspace.WorkspaceID == "" {
			return "", false, fmt.Errorf("Herdr workspace for %q has no workspace ID", targetPath)
		}
		if match != "" {
			return "", false, fmt.Errorf("multiple Herdr workspaces match checkout path %q", targetPath)
		}
		match = workspace.WorkspaceID
	}
	return match, match != "", nil
}

func CloseWorkspace(workspaceID string) error {
	if workspaceID == "" {
		return fmt.Errorf("cannot close Herdr workspace: workspace ID is empty")
	}
	output, err := commands.CombinedOutput("herdr", "workspace", "close", workspaceID)
	if err != nil {
		return fmt.Errorf("failed to close Herdr workspace %q: %w: %s",
			workspaceID, err, strings.TrimSpace(string(output)))
	}
	var response responseEnvelope
	if err := json.Unmarshal(output, &response); err != nil {
		return fmt.Errorf("failed to parse Herdr workspace close response for %q: %w", workspaceID, err)
	}
	if response.Result.Type != "workspace_closed" {
		return fmt.Errorf("unexpected Herdr workspace close response type %q for %q", response.Result.Type, workspaceID)
	}
	return nil
}
