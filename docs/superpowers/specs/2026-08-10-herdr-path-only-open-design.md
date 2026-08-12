# Herdr Path-Only Workspace Open Design

## Context

Ygg opens a Git worktree in Herdr by running `herdr worktree open`. The current implementation passes the target worktree path and also scopes the request with `--workspace "$HERDR_WORKSPACE_ID"`.

That workspace ID describes the environment from which ygg was launched, not the worktree being opened. Inside tmux it can be stale because the tmux server preserves the environment from when it started, and Herdr can associate the pane with the tmux client's working directory rather than the shell's working directory. Herdr then validates an unrelated, non-Git originating workspace and rejects an otherwise valid target with `not_git_worktree`.

Herdr can infer the repository from `--path`. Opening the same target without `--workspace` succeeds independently of the caller's workspace state.

## Decision

Herdr open operations will be path-only. `OpenWorkspace` will run:

```text
herdr worktree open \
  --path <absolute-worktree-path> \
  --label <branch-or-worktree-name> \
  --focus
```

It will not read, validate, or pass `HERDR_WORKSPACE_ID`.

This supersedes the originating-workspace requirement for open operations in `2026-08-10-herdr-workspace-integration-design.md`. The rest of that design remains unchanged.

## Architecture and Responsibilities

`internal/herdr.OpenWorkspace(path, branch, worktreeName)` remains the single boundary for opening a target in Herdr. Its responsibilities are:

- Derive the label with the existing `WorkspaceLabel` rule: prefer the full branch name and fall back to the worktree name.
- Ask Herdr to open and focus the checkout identified by `path`.
- Validate Herdr's command result and response envelope.

The target path is sufficient repository context. Removing the ambient source-workspace dependency makes the operation mean exactly what its inputs express: open this checkout with this label.

Callers in `internal/multiplexer` and `internal/cli` do not change. `new` and `switch` continue to pass the structured target and continue to fall back to a subshell if Herdr opening fails.

## Data Flow

1. `new` or `switch` resolves a Git worktree target.
2. The multiplexer layer selects the active Herdr backend.
3. The backend passes the target path, branch, and worktree name to `OpenWorkspace`.
4. `OpenWorkspace` constructs one path-only `herdr worktree open` command.
5. Herdr infers repository ownership from the target checkout path, opens or focuses the workspace, and returns its workspace ID.
6. Ygg validates the response and returns success. On failure, the existing CLI flow reports the error and enters the target through a subshell.

There is no workspace-scoped first attempt and no fallback retry.

## Error Handling

The change removes two open-operation failure conditions:

- Missing `HERDR_WORKSPACE_ID` is no longer an error.
- A stale or unrelated `HERDR_WORKSPACE_ID` cannot affect the command.

All target-related and response-related failures retain their current behavior:

- A non-zero Herdr command result includes the target path, execution error, and trimmed command output.
- Malformed JSON is rejected with target context.
- A response type other than `worktree_opened` is rejected.
- A successful response without a workspace ID is rejected.

Ygg makes one call only. It does not parse `not_git_worktree`, classify Herdr errors, or retry with alternate arguments.

## Cleanup Boundary

Removal and cleanup are out of scope. Their existing use of `HERDR_WORKSPACE_ID` serves a different purpose: determining whether closing a matched target workspace also handled navigation away from the caller's current workspace.

This design does not introduce a global rule against `HERDR_WORKSPACE_ID`. Each operation depends only on the context it needs.

## Testing

Focused `internal/herdr` tests will verify:

- The exact open command contains `--path`, the derived `--label`, and `--focus`, and omits `--workspace`.
- Opening succeeds when `HERDR_WORKSPACE_ID` is unset.
- A stale `HERDR_WORKSPACE_ID` is ignored and does not change the command.
- Branch and detached-worktree label selection remains unchanged.
- Command errors, malformed responses, unexpected response types, and missing returned workspace IDs remain errors.

The full Go test suite will verify that multiplexer selection, CLI fallback, and removal behavior remain intact.

## Scope

Implementation is limited to the Herdr open command and its focused tests. Documentation changes record the new path-only contract. There is no retry mechanism, tmux environment management, cleanup redesign, or unrelated refactoring.
