# Tmux and Multiplexer Integration

## Summary

Ygg will support tmux windows with the same lifecycle behavior and naming convention as its existing Zellij tabs. When invoked inside tmux, `ygg new` and `ygg switch` will create or focus a window named `<repo>/<worktree>` in the current tmux session. `ygg remove` and `ygg clean` will close the corresponding window after successfully removing its worktree.

Multiplexer selection will be automatic. Tmux takes precedence over Zellij when both environments are present, Zellij remains the second choice, and the existing subshell behavior remains the fallback when no multiplexer is active or opening a multiplexer workspace fails.

## Goals

- Provide full `new`, `switch`, `remove`, and `clean` lifecycle parity between tmux and Zellij.
- Use the existing `<repo>/<worktree>` workspace naming convention for both multiplexers.
- Operate only on windows in the current tmux session.
- Detect active multiplexers without configuration or setup.
- Centralize multiplexer selection so CLI commands do not contain backend-specific branching.
- Preserve current Zellij behavior and its fallback semantics.
- Keep the abstraction internal to ygg so its implementation can evolve without creating a public Go API commitment.

## Non-goals

- Creating, attaching to, or managing tmux sessions.
- Adding a user-selected default multiplexer or configuration file.
- Operating on tmux windows in other sessions.
- Supporting use of a multiplexer binary when ygg is invoked outside that multiplexer.
- Publishing a reusable external Go library.
- Changing worktree creation, branch selection, file copying, or shell-wrapper behavior.

## Architecture

### Generic multiplexer layer

Add `internal/multiplexer`, which exposes the integration point used by the CLI. It defines a small backend contract:

```go
type Backend interface {
    Name() string
    Active() bool
    Open(dir, repoName, worktreeName string) error
    Close(repoName, worktreeName string) error
}
```

The package owns backend selection and returns the first active backend from this ordered list:

1. tmux
2. Zellij
3. no backend

This order makes the innermost tmux session win when tmux is run from inside Zellij. Selection is based only on environment variables: a non-empty `TMUX` activates tmux and a non-empty `ZELLIJ` activates Zellij.

The existing `YGG_SHELL` path remains ahead of multiplexer detection. When ygg is already running in a ygg-created subshell, `new` and `switch` continue to emit a `cd` command for the shell wrapper rather than opening a multiplexer workspace.

### Backend boundaries

Add `internal/tmux` for tmux detection, window lookup, opening, focusing, and closing.

Keep `internal/zellij` as the owner of the existing implementation. The generic layer uses a thin adapter that delegates to `zellij.InZellij`, `zellij.OpenTab`, and `zellij.CloseTab`. The Zellij command sequence, shell selection, working-directory handling, lookup behavior, and error semantics will not be rewritten as part of this feature.

Both backends follow the same naming contract: concatenate the repository and worktree names as `<repo>/<worktree>`. The existing `zellij.TabName` behavior remains covered by its current tests, and tmux naming is covered with the same representative cases.

## Command Flows

### `ygg new <name>`

1. Create the branch and worktree using the existing flow.
2. If `YGG_SHELL=1`, emit the existing `cd` instruction and stop.
3. Detect the active multiplexer.
4. If a backend is active, ask it to open the workspace.
5. If opening succeeds, return without spawning a subshell.
6. If opening fails, report the backend-specific failure and enter the worktree with the existing subshell behavior.
7. If no backend is active, use the existing subshell behavior.

### `ygg switch <name>`

Resolve the worktree, retain the existing `YGG_SHELL` behavior, and then follow the same detect/open/fallback sequence as `new`. An existing multiplexer workspace is focused; a missing one is created.

### `ygg remove [name]`

Perform all existing safety checks and remove the worktree first. After successful removal, detect the active backend and ask it to close the matching workspace. A close failure is reported but does not turn the completed worktree removal into an error.

When the command closes the tmux window in which it is running, tmux selects another window according to its normal session behavior. Ygg does not spawn a replacement shell in the deleted directory.

### `ygg clean`

Detect the active backend once before processing selected worktrees. After each successful worktree removal, ask that backend to close the matching workspace. Failures are reported per workspace and cleanup continues.

Dry runs do not perform multiplexer operations.

## Tmux Backend

### Detection and scope

`InTmux` returns true when `TMUX` is non-empty. All tmux commands omit an explicit session target, causing tmux to resolve the session from the invoking client/pane context. Ygg never enumerates or mutates another session.

### Exact window lookup

The backend obtains the current session's windows with `tmux list-windows -F`, requesting `#{window_id}` and `#{window_name}` separated by a literal tab. It splits each output line at the first tab and compares the complete window name to `<repo>/<worktree>`.

Subsequent operations target the stable window ID rather than the human-readable name. This prevents tmux target parsing from treating punctuation in repository or branch names as target syntax.

Lookup has three outcomes:

- No exact match: the window does not exist.
- One exact match: return its window ID.
- More than one exact match: return an ambiguity error and do not select or close either window.

Ygg-created windows will not normally duplicate because lookup happens before creation. Treating manually-created duplicates as ambiguous avoids acting on an arbitrary window.

### Open

If lookup returns one match, run `tmux select-window -t <window-id>`.

If lookup returns no match, run `tmux new-window -n <repo/worktree> -c <worktree-path>`. Tmux creates the window in the current session, starts its normal configured shell or default command in the worktree directory, and focuses the new window.

### Close

If lookup returns one match, run `tmux kill-window -t <window-id>`. If no match exists, return success without running another command. Ambiguous lookup and command failures are returned to the caller.

### Command execution seam

Tmux command execution will use a private injectable runner. Production code delegates to `os/exec`; unit tests supply a fake runner to validate command arguments, simulate formatted window lists, and exercise failures without requiring a live tmux server. This test seam is not part of the public package API.

## Error Handling

- Failure to list tmux windows, select a window, create a window, or kill a window returns a contextual error naming the failed operation and workspace.
- An `Open` error causes the CLI to report the error and fall back to `shell.Spawn`, matching existing Zellij behavior.
- A `Close` error is best-effort: the CLI reports it and continues because the worktree has already been removed.
- If both `TMUX` and `ZELLIJ` are set and tmux fails, ygg falls back to a subshell. It does not operate on the outer Zellij session after choosing tmux.
- Closing a workspace that is already absent succeeds.
- No new rollback behavior is added for worktree creation or removal.

## CLI and Documentation Changes

- Replace direct Zellij checks in `new`, `switch`, `remove`, and `clean` with calls through `internal/multiplexer`.
- Make status and fallback messages identify the selected backend rather than hard-coding Zellij.
- Update command help that currently describes only subshell behavior so it also accounts for multiplexer workspaces.
- Expand the README integration documentation to cover tmux and Zellij, automatic detection, current-session tmux scope, full lifecycle behavior, and tmux-first precedence when nested.
- Update the bundled ygg agent skill to describe automatic tmux/Zellij window handling rather than Zellij alone.

No user-facing configuration, setup command, or new CLI flag is introduced.

## Testing

### Tmux unit tests

- `TMUX` set and unset detection.
- `<repo>/<worktree>` naming using the same cases as Zellij.
- Exact parsing and matching of formatted window listings.
- Selection of an existing window by ID.
- Creation of a missing window with the expected name and working directory.
- Closing an existing window by ID.
- Successful no-op when closing a missing window.
- Ambiguity errors for duplicate exact names without a select or kill command.
- Contextual errors for list, select, create, and kill failures.

### Multiplexer selector tests

- tmux is selected when only `TMUX` is set.
- Zellij is selected when only `ZELLIJ` is set.
- tmux is selected when both variables are set.
- no backend is returned when neither variable is set.
- the Zellij adapter delegates without altering arguments or errors.

### CLI behavior tests

- `new` and `switch` use an active backend and do not spawn a subshell after a successful open.
- backend open failures reach the existing subshell fallback.
- `remove` and `clean` request closure only after successful worktree removal.
- close failures are reported without stopping later cleanup work.

The implementation must pass `go test ./...`. Manual smoke checks will verify creation, focus, working directory, removal, and cleanup inside a live tmux session. A Zellij smoke check will verify that creation/focus and working-directory behavior remain unchanged when Zellij is available.

## Acceptance Criteria

- Inside tmux, `ygg new my-feature` creates and focuses a current-session window named `<repo>/my-feature` rooted at the new worktree.
- Inside tmux, `ygg switch my-feature` focuses the exact matching current-session window or creates it when absent.
- Inside tmux, `ygg remove` and `ygg clean` close a unique exact-matching window after successful worktree removal.
- Tmux operations never target another session and never choose arbitrarily between duplicate exact names.
- Inside Zellij without tmux, all previously supported behaviors and commands remain unchanged.
- Outside both multiplexers, ygg retains its existing subshell behavior.
- Inside nested tmux and Zellij, tmux is selected.
- A multiplexer open failure falls back to a subshell; a close failure remains non-fatal.
- No configuration is required.
