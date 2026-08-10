# Herdr Workspace Integration

## Summary

Ygg will support Herdr as a native workspace backend alongside tmux and Zellij. When ygg runs inside Herdr, `ygg new <name>` will keep using ygg's existing Git worktree creation flow and then open the resulting checkout as a focused Herdr worktree workspace. The workspace label will be the full Git branch name, without the repository prefix, because Herdr already presents the repository as the group heading.

`ygg switch` will focus an already-open Herdr worktree workspace or open it when it is closed. `ygg remove` and `ygg clean` will close the matching Herdr workspace only after ygg successfully removes the Git checkout.

Herdr will have the highest workspace-backend priority. The effective routing order will be:

1. Herdr
2. Existing ygg-shell `cd` behavior
3. tmux
4. Zellij
5. Subshell fallback

This ordering ensures that an inherited `YGG_SHELL=1` does not prevent ygg from opening a requested workspace when it is running in a Herdr-managed pane.

## Goals

- Make `ygg new`, `ygg switch`, `ygg remove`, and `ygg clean` understand Herdr workspaces.
- Preserve ygg as the owner of Git worktree creation, base selection, file copying, safety checks, removal, and directory layout.
- Register ygg-created checkouts with Herdr through Herdr's native worktree API so they receive worktree provenance and repository grouping.
- Label each Herdr child workspace with the full worktree branch name, such as `xyz` or `feat/auth`.
- Identify Herdr workspaces by exact checkout path rather than display label.
- Preserve existing tmux, Zellij, ygg-shell, and subshell behavior except for the approved Herdr-over-ygg-shell precedence case.
- Keep failures recoverable: opening falls back to a subshell, while closure failures remain non-fatal after successful Git removal.

## Non-goals

- Delegating Git worktree creation or deletion to Herdr.
- Replacing ygg's `.worktrees/<name>` layout with Herdr's configured worktree directory.
- Managing Herdr sessions, servers, tabs, panes, or agents.
- Opening Herdr workspaces when ygg is not running in a Herdr-managed pane.
- Adding a user-selected backend, configuration file, or command-line flag.
- Renaming the primary repository workspace automatically.
- Changing tmux or Zellij workspace naming.
- Supporting arbitrary older Herdr releases that do not expose native worktree commands and worktree provenance.

## Ownership Boundary

Ygg remains the sole owner of Git mutations. In particular, `ygg new` continues to:

1. Fetch `origin` on a best-effort basis.
2. Resolve the default branch and effective base ref.
3. Create or check out the requested branch in `.worktrees/<name>`.
4. Copy eligible untracked and ignored files from the primary checkout.
5. Warn when `.worktrees` is not ignored.

Herdr begins only after the checkout exists. It owns the terminal workspace presentation: registering the existing checkout, grouping it with the repository, assigning its label, focusing it, and closing its workspace state.

This boundary keeps worktree behavior consistent across Herdr, tmux, Zellij, and plain shells. It also avoids duplicating ygg's base-ref and cleanup policies with Herdr's separate worktree creation and removal commands.

## Generic Workspace Backend

The existing `internal/multiplexer` package remains the internal selection and delegation layer. Herdr is a workspace manager rather than only a terminal multiplexer, but renaming this established internal package is unrelated to the feature and is not required.

Replace the backend's positional workspace arguments with a structured target containing:

- Absolute checkout path.
- Repository name.
- Worktree name.
- Full branch name.

The full branch and worktree name remain distinct. For example, a checkout stored at `.worktrees/feat/auth` may have the worktree name `auth` while its branch is `feat/auth`. Herdr uses the branch for its display label. Tmux and Zellij retain their current `<repo>/<worktree>` convention. If an externally created checkout is detached and has no branch, Herdr falls back to the worktree name for the label; ygg-created worktrees are always attached to a branch.

The backend contract will support two phases for closure:

1. Prepare a close operation while the checkout and backend metadata still exist.
2. Execute the prepared operation only after Git removal succeeds.

A prepared operation records whether a workspace was found and retains the backend's stable target, such as a Herdr workspace ID or tmux window ID. This avoids looking up a Herdr workspace after its checkout path has been deleted and preserves the rule that a failed Git removal must never close the workspace.

Backends continue to expose a human-readable name for status and error messages. All interfaces remain internal to ygg.

## Detection and Precedence

Add `internal/herdr` with `InHerdr`, which returns true only when `HERDR_ENV=1`. An active Herdr backend requires `HERDR_WORKSPACE_ID` for open operations; a missing ID becomes a contextual open error rather than silently targeting whichever workspace another client has focused.

Backend selection order is:

1. Herdr when `HERDR_ENV=1`.
2. tmux when `TMUX` is non-empty.
3. Zellij when `ZELLIJ` is non-empty.
4. No backend.

For `new` and `switch`, Herdr detection occurs before the existing `YGG_SHELL` short circuit. If Herdr is not active, `YGG_SHELL=1` retains its current behavior and emits a `cd` instruction before tmux or Zellij selection. Thus the complete behavioral priority is Herdr, ygg-shell, tmux, Zellij, then subshell.

Once ygg selects an active backend, it does not cascade to lower-priority backends after an error. A Herdr open error falls back to the existing subshell behavior instead of trying an outer tmux or Zellij session.

## Herdr Backend

### Command execution

The backend invokes the installed `herdr` executable directly with an argument slice. It does not construct shell command strings or communicate with the socket protocol itself. A private injectable command runner provides deterministic unit tests without requiring a live Herdr server.

Ygg relies on the JSON emitted by Herdr's supported CLI commands. Non-zero exits, malformed JSON, missing required fields, and unexpected response types become contextual backend errors.

### Open or focus

Given a target checkout, the backend runs the equivalent of:

```bash
herdr worktree open \
  --workspace "$HERDR_WORKSPACE_ID" \
  --path "/absolute/path/to/worktree" \
  --label "<full-branch-name>" \
  --focus
```

`--workspace` binds the operation to the caller's Herdr repository group instead of relying on another client's focused state. `--path` lets Herdr register the existing checkout created by ygg. `--label` applies exactly the branch name requested by the user, and `--focus` gives `new` and `switch` their expected navigation behavior.

Herdr's `worktree open` operation is idempotent for an already-open checkout: it returns and focuses the existing workspace rather than creating a duplicate. Ygg does not search or focus by label.

### Prepare closure

Before Git removal, the backend runs `herdr workspace list` and parses each workspace's optional worktree provenance. It compares the target's absolute cleaned checkout path with `worktree.checkout_path`.

Lookup has three results:

- No exact match: prepare a successful no-op.
- One exact match: retain its opaque `workspace_id` in the close operation.
- More than one exact match: return an ambiguity error and do not select a workspace to close.

Labels, workspace ordering, and numeric display positions never participate in matching. A manually renamed workspace remains discoverable through its checkout provenance.

### Execute closure

After ygg successfully removes the checkout, execute a matched close operation with:

```bash
herdr workspace close <workspace-id>
```

An unmatched no-op succeeds without running a close command. A close failure is reported but does not undo or convert the completed Git removal into a failure.

When the matched workspace is the caller's current workspace, Herdr owns the navigation caused by closing it. Ygg does not start a replacement subshell after a successful current-workspace close. If no Herdr workspace was matched or closure fails while the command remains alive, ygg changes to the primary checkout before using its existing fallback shell behavior, so it never starts a shell in the deleted directory.

## Command Flows

### `ygg new <name>`

1. Perform the existing fetch, default-branch, create, copy, and warning flow.
2. Build a workspace target from the new checkout, including the full branch name.
3. If Herdr is active, open or focus the Herdr worktree workspace.
4. If Herdr succeeds, return without spawning a subshell.
5. If Herdr fails, report the error and enter the checkout with the existing subshell behavior.
6. When Herdr is inactive, retain the existing ygg-shell, tmux, Zellij, and subshell routing.

### `ygg switch <name>`

1. Resolve the worktree through the existing manager.
2. Build a target using the resolved checkout path and full `Worktree.Branch` value.
3. Follow the same routing and open/fallback behavior as `new`.

An open Herdr workspace is focused. A closed worktree checkout is opened as a new focused workspace in the same repository group.

### `ygg remove [name]`

1. Resolve the target worktree and perform all existing primary-worktree, dirty-state, and merge checks.
2. Detect the active workspace backend.
3. Ask the backend to prepare an exact close operation. Report preparation errors but do not prevent an otherwise valid Git removal.
4. Remove the checkout through ygg.
5. If removal succeeds, execute the prepared close operation.
6. If removal fails, discard the prepared operation and leave the workspace open.
7. Preserve the existing return-to-primary behavior unless a successfully closed current backend workspace already handled navigation.

### `ygg clean`

Keep the existing selection, dry-run, and confirmation behavior. After confirmation, prepare and execute closure independently for each selected checkout around its Git removal. A preparation, removal, or close failure for one worktree is reported without preventing later eligible worktrees from being processed.

A dry run performs no Herdr or other backend closure calls.

## Error Handling

- A missing `HERDR_WORKSPACE_ID` while `HERDR_ENV=1` is an open error naming the missing caller context.
- Failure to start `herdr`, connect to its server, or run a command returns a contextual error naming the operation and target.
- Invalid JSON or a response without the required type and fields returns a parsing error; ygg never guesses an ID.
- Open errors trigger the existing subshell fallback after the checkout has been created or resolved.
- Close-preparation errors are warnings and produce no close operation. Git removal may continue.
- Close-execution errors are warnings and never roll back successful Git removal.
- No-match closure is idempotent success.
- Duplicate exact path matches are treated as ambiguous; ygg closes neither workspace.
- Backend errors never cause ygg to operate on a lower-priority nested backend.

## CLI and Documentation Changes

- Update `new` and `switch` help to mention Herdr, tmux, and Zellij workspaces.
- Update the README's terminal workspace integration section with Herdr detection through `HERDR_ENV`, full-branch Herdr labels, repository grouping, lifecycle behavior, routing precedence, and fallback semantics.
- Document Herdr 0.6.2 or newer as the minimum supported version for this integration because that release introduced the native worktree CLI and workspace provenance used by ygg.
- Update the bundled ygg agent skill so agents understand the Herdr routing and naming behavior.
- Preserve the existing tmux/Zellij `<repo>/<worktree>` documentation and behavior.

No configuration file, setup command, or new ygg flag is introduced.

## Testing

### Herdr unit tests

- `HERDR_ENV=1` and all other values.
- Label selection from a full branch name and detached-checkout fallback.
- Exact `worktree open` arguments: source workspace ID, absolute checkout path, full branch label, and focus.
- Missing `HERDR_WORKSPACE_ID` handling.
- Successful open of a new workspace and an already-open workspace response.
- Command-start, non-zero exit, malformed JSON, unexpected response type, and missing-field failures.
- Exact checkout-path lookup in `workspace list` output.
- No-match no-op, unique match, and duplicate-match ambiguity.
- Close by opaque workspace ID and contextual close failures.

### Backend and CLI tests

- Herdr wins when tmux and/or Zellij markers are also present.
- tmux and Zellij retain their existing relative priority when Herdr is absent.
- Herdr wins over an inherited `YGG_SHELL=1`; ygg-shell still wins over tmux and Zellij when Herdr is absent.
- Structured targets preserve checkout path, worktree name, and full branch separately.
- `new` and `switch` do not spawn a subshell after a successful Herdr open.
- Herdr open failures reach the subshell fallback.
- Closure preparation happens after safety checks but before Git removal.
- Prepared closure executes only after successful Git removal.
- Current-workspace removal does not spawn a replacement shell after successful backend closure.
- `clean` continues after individual preparation, removal, and close failures.
- Dry runs perform no backend closure operations.
- Existing tmux, Zellij, ygg-shell, and plain-subshell tests remain green.

### Verification

- Run Go formatting on changed Go files.
- Run `go vet ./...`.
- Run `go test ./...`.
- Run `git diff --check`.
- Build a disposable ygg binary for live smoke testing.

Use a disposable Git repository and Herdr test workspace or named test session for smoke testing. Create and close only workspaces created by the test. Verify:

- `ygg new xyz` creates `.worktrees/xyz` and focuses a child workspace rooted there and labeled `xyz`.
- A branch such as `feat/auth` keeps the complete `feat/auth` workspace label.
- `ygg switch` focuses the existing workspace without duplication and reopens it after workspace-only closure.
- `ygg remove` closes only the exact matching child workspace after checkout removal.
- `ygg clean` closes each successfully removed worktree workspace.
- The primary repository workspace remains intact.

## Acceptance Criteria

- Inside Herdr, `ygg new xyz` creates the worktree through ygg and opens a focused, worktree-backed Herdr child workspace labeled `xyz`.
- The Herdr workspace starts in the exact ygg-created checkout path.
- Herdr labels use the full branch name without a repository prefix.
- `ygg switch` focuses an already-open checkout workspace or opens it when closed, without duplicates.
- `ygg remove` and `ygg clean` close only the workspace whose provenance exactly matches the removed checkout.
- No backend workspace is closed when Git removal fails.
- Herdr takes precedence over inherited ygg-shell, tmux, and Zellij markers.
- A Herdr open failure reports the cause and falls back to a subshell; closure failures remain non-fatal.
- Tmux, Zellij, ygg-shell, and plain-subshell behavior remain unchanged outside the approved precedence case.
- Ygg remains the only component that creates or removes Git worktrees in these command flows.
- Automated tests, vetting, formatting checks, and the disposable Herdr smoke test pass.
