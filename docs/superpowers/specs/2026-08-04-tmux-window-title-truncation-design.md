# Tmux Window Title Truncation

## Summary

Shorten window names shaped like `<repo>/<worktree>` only when tmux renders them in the status bar. Keep the complete underlying window name so ygg can continue to identify, select, and close the correct worktree window.

The rendered worktree portion keeps at most five hyphens. For example:

```text
helium/hel-142-members-table-extends-beyond-its-card-boundary
```

is displayed as:

```text
helium/hel-142-members-table-extends-beyond
```

Names with five or fewer hyphens remain unchanged.

## Scope

- Update `/home/jpathm/.tmux.conf`.
- Apply the same shortened label to inactive and active windows.
- Apply the rule to every worktree name, not only Linear-style names.
- Preserve the repository portion before `/` exactly as written.
- Do not change ygg, branch names, worktree directory names, or actual tmux window names.

## Design

Replace `#W` in `window-status-format` and `window-status-current-format` with a tmux format substitution over `window_name`.

Use this extended regular expression and replace a match with its first capture group:

```text
^([^/]*/([^-]*-){5}[^-]*)-.*$
```

The extended regular expression captures the repository name, slash, and worktree text through its fifth hyphen. If a sixth hyphen exists, that hyphen and the remaining suffix are removed from the rendered value. If the expression does not match, tmux leaves the value unchanged.

This display-only approach avoids collisions. Two worktrees whose names share the same visible prefix may look identical in the status bar, but their complete internal names remain distinct and ygg's exact-name lookup continues to operate correctly.

## Error Handling

Tmux parses the format when the configuration is loaded. A syntax error should be detected by loading the configuration in an isolated tmux server before reloading the live configuration. If live reload fails, restore the two original format lines containing `#W`.

## Verification

- Parse the updated configuration with an isolated tmux server.
- Confirm a long name is rendered through the fifth hyphen only.
- Confirm a name containing five hyphens is unchanged.
- Confirm a short name is unchanged.
- Reload the live tmux configuration after the isolated checks pass.
