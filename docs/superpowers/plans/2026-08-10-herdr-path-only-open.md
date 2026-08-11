# Herdr Path-Only Workspace Open Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Herdr workspace opening depend only on the target worktree path so unset or stale caller workspace IDs cannot break `ygg new` or `ygg switch`.

**Architecture:** Keep `internal/herdr.OpenWorkspace` as the sole Herdr open boundary, but construct one `herdr worktree open` call without `--workspace`. Preserve its public signature, label selection, response validation, CLI fallback, and all cleanup behavior.

**Tech Stack:** Go 1.24.0, standard library `os/exec` and `encoding/json`, Go `testing` package.

## Global Constraints

- Invoke Herdr exactly once with `worktree open --path <path> --label <label> --focus`.
- `OpenWorkspace` must not read, validate, or pass `HERDR_WORKSPACE_ID`.
- Do not add a scoped first attempt, retry path, or Herdr error classification.
- Preserve command-error context and JSON response validation.
- Leave removal, cleanup, multiplexer selection, and CLI fallback behavior unchanged.
- Do not perform unrelated refactoring.

---

## File Structure

- Modify `internal/herdr/herdr_test.go`: replace the caller-workspace contract tests with path-only command tests covering both unset and stale environment values.
- Modify `internal/herdr/herdr.go`: remove the originating-workspace requirement and the `--workspace` command arguments from `OpenWorkspace`.
- No caller or cleanup files change because the `OpenWorkspace(path, branch, worktreeName string) error` interface remains stable.

### Task 1: Open Herdr Workspaces by Target Path

**Files:**
- Modify: `internal/herdr/herdr_test.go:67-98`
- Modify: `internal/herdr/herdr.go:52-79`
- Reference: `docs/superpowers/specs/2026-08-10-herdr-path-only-open-design.md`

**Interfaces:**
- Consumes: existing `commandRunner.CombinedOutput(name string, args ...string) ([]byte, error)`, `WorkspaceLabel(branch, worktreeName string) string`, and Herdr `worktree_opened` JSON responses.
- Produces: unchanged `OpenWorkspace(path, branch, worktreeName string) error`; its command contract becomes path-only and independent of `HERDR_WORKSPACE_ID`.

- [ ] **Step 1: Replace the caller-workspace tests with failing path-only contract tests**

Replace `TestOpenWorkspaceUsesCallerGroupPathBranchAndFocus` and `TestOpenWorkspaceRequiresCallerWorkspace` in `internal/herdr/herdr_test.go` with:

```go
func TestOpenWorkspaceUsesTargetPathLabelAndFocus(t *testing.T) {
	for _, tt := range []struct {
		name        string
		workspaceID string
	}{
		{name: "unset caller workspace"},
		{name: "stale caller workspace", workspaceID: "stale-workspace"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HERDR_WORKSPACE_ID", tt.workspaceID)
			fake := &fakeRunner{results: []commandResult{{
				output: `{"id":"cli","result":{"type":"worktree_opened","workspace":{"workspace_id":"w5"}}}`,
			}}}
			useRunner(t, fake)

			err := OpenWorkspace("/repo/.worktrees/auth", "feat/auth", "auth")
			if err != nil {
				t.Fatalf("OpenWorkspace() error = %v", err)
			}
			want := commandCall{name: "herdr", args: []string{
				"worktree", "open",
				"--path", "/repo/.worktrees/auth",
				"--label", "feat/auth", "--focus",
			}}
			if !reflect.DeepEqual(fake.calls, []commandCall{want}) {
				t.Fatalf("calls = %#v, want %#v", fake.calls, []commandCall{want})
			}
		})
	}
}
```

- [ ] **Step 2: Run the focused test and verify both old assumptions fail**

Run:

```bash
go test ./internal/herdr -run '^TestOpenWorkspaceUsesTargetPathLabelAndFocus$' -v
```

Expected: FAIL. The `unset_caller_workspace` subtest reports the missing `HERDR_WORKSPACE_ID` error, and the `stale_caller_workspace` subtest reports an argument mismatch containing `--workspace stale-workspace`.

- [ ] **Step 3: Implement the path-only open command**

Replace `OpenWorkspace` in `internal/herdr/herdr.go` with:

```go
func OpenWorkspace(path, branch, worktreeName string) error {
	output, err := commands.CombinedOutput(
		"herdr", "worktree", "open",
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
```

Keep the `os` import because `InHerdr` still reads `HERDR_ENV`; cleanup's separate environment-dependent behavior remains unchanged elsewhere.

- [ ] **Step 4: Format the modified Go files**

Run:

```bash
gofmt -w internal/herdr/herdr.go internal/herdr/herdr_test.go
```

Expected: command exits successfully with no output.

- [ ] **Step 5: Run the focused Herdr open tests**

Run:

```bash
go test ./internal/herdr -run '^Test(OpenWorkspace|WorkspaceLabel)' -v
```

Expected: PASS. Both environment variants use the same path-only command, label selection passes, and all existing command/response failure cases remain validated.

- [ ] **Step 6: Run the full test suite**

Run:

```bash
go test ./...
```

Expected: PASS for every package, confirming multiplexer selection, CLI fallback, and cleanup behavior are unchanged.

- [ ] **Step 7: Review the final diff for scope and whitespace errors**

Run:

```bash
git diff --check
git diff -- internal/herdr/herdr.go internal/herdr/herdr_test.go
```

Expected: `git diff --check` prints nothing. The diff contains only the two contract-test replacements and removal of the open path's workspace-ID check and arguments.

- [ ] **Step 8: Commit the implementation**

Run:

```bash
git add internal/herdr/herdr.go internal/herdr/herdr_test.go
git commit -m "fix: open Herdr workspaces by target path"
```

Expected: one commit containing only the Herdr open implementation and focused tests.
