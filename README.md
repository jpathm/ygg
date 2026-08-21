# ygg - Git Worktree Helper

A simple CLI tool for managing git worktrees. Create feature branches in isolated directories, switch between them easily, and clean up when done.

**[Watch the demo video](https://youtu.be/8Q8iZ4TkUUc)**

[![Demo video](https://img.youtube.com/vi/8Q8iZ4TkUUc/maxresdefault.jpg)](https://youtu.be/8Q8iZ4TkUUc)

## Installation

### Homebrew

```bash
brew tap joch/ygg
brew trust joch/ygg   # Homebrew 6.0.0+ requires trusting non-official taps
brew install ygg
```

### Go

```bash
go install github.com/joch/ygg/cmd/ygg@latest
```

### From source

```bash
go build -o ygg ./cmd/ygg
```

## Usage

### Create a new worktree

```bash
ygg new my-feature
```

This will:
1. Fetch latest from origin
2. Create a new worktree with branch `my-feature` based on the default branch
3. Open the worktree in the active Herdr/tmux/Zellij workspace manager, or enter a subshell

Worktrees are created at `.worktrees/<feature-name>` inside the repository root.

### List worktrees

```bash
ygg list
```

Shows all worktrees. Current worktree is marked with `*`, modified ones show `[modified]`.

### Switch to a worktree

```bash
ygg switch my-feature
```

Focuses or creates a workspace when a supported workspace manager is active. Otherwise, enters a subshell in the specified worktree.

### Remove a worktree

```bash
ygg remove my-feature  # remove by name
ygg remove             # remove current worktree
ygg rm my-feature      # alias
```

Use `--force` to remove even with uncommitted changes or unmerged branches.

### Clean up merged worktrees

```bash
ygg clean           # prompts for confirmation
ygg clean --dry-run # show what would be removed
ygg clean --force   # no confirmation
```

Removes worktrees whose branches have been merged to main.

## Commands

| Command | Description |
|---------|-------------|
| `ygg new <name>` | Create a new worktree and enter it |
| `ygg list` | List all worktrees |
| `ygg switch <name>` | Switch to a worktree |
| `ygg remove [name]` | Remove a worktree |
| `ygg clean` | Remove merged worktrees |

## Linear integration

`ygg new` links worktrees to Linear issues. It is optional, and it never
prevents a worktree from being created.

A name that looks like a Linear branch or identifier is verified and used
exactly as typed:

```sh
ygg new snk-31-owl-have-cli-also-host-pure-html
# ℹ Linked to SNK-31 — OWL - have cli also host pure html
```

Any other name creates an issue and adopts its branch name:

```sh
ygg new unified-tui
# ℹ Created SNK-42 — https://linear.app/gridkit/issue/SNK-42/unified-tui
# ✓ Created worktree at .worktrees/snk-42-unified-tui
```

### Setup

Export a Linear personal API key, created under Settings → API in Linear:

```sh
export LINEAR_API_KEY=lin_api_...
```

Then map repositories onto Linear teams in `~/.config/ygg/config.json`:

```json
{
  "linear": {
    "defaultTeam": "SKUNK",
    "teams": {
      "GridKitLLC/otter-tools": "SKUNK",
      "GridKitLLC/ygg": "SKUNK"
    }
  }
}
```

Keys are `owner/repo`, matched against the `origin` remote; SSH and HTTPS
remotes both work. `defaultTeam` is used when no entry matches. The API key is
never read from this file.

### When Linear is unavailable

Every one of these prints a warning and still creates the worktree:

| Situation | Result |
| --- | --- |
| `LINEAR_API_KEY` unset | Unlinked worktree |
| Repository unmapped and no `defaultTeam` | Unlinked worktree |
| `config.json` malformed | Warning, then treated as unconfigured |
| Linear unreachable | Unlinked worktree |
| API key rejected | Unlinked worktree |
| Named issue does not exist | Worktree created under the name as typed |

## Shell Completion

```bash
# Bash
source <(ygg completion bash)

# Zsh
source <(ygg completion zsh)

# Fish
ygg completion fish | source
```

Add to your shell rc file for persistent completion.

## Prompt Integration

When inside a ygg shell, `$YGG_WORKTREE` is set to the current worktree name. Add to your prompt:

```bash
# Bash/Zsh
PS1='${YGG_WORKTREE:+[$YGG_WORKTREE] }'$PS1
```

## Workspace Integration

When running inside [Herdr](https://herdr.dev/) 0.6.2 or later, [tmux](https://github.com/tmux/tmux/wiki), or [Zellij](https://zellij.dev/), ygg opens or focuses a workspace instead of spawning a subshell. Herdr is detected via `HERDR_ENV=1`; it uses native grouped worktree workspaces labeled with the full branch name, such as `xyz` or `feat/auth`. Tmux and Zellij are detected via `$TMUX` and `$ZELLIJ` and keep their `<repo>/<worktree>` workspace names. Tmux operations are limited to the current session.

- `ygg new my-feature` creates or focuses the worktree workspace
- `ygg switch my-feature` focuses the workspace, or reopens it if needed
- `ygg remove` and `ygg clean` close matching workspaces only after successful worktree removal

Workspace backends are selected in this order: Herdr → ygg-shell → tmux → Zellij → subshell. Inside a ygg shell, `ygg new` and `ygg switch` keep their existing `cd` behavior before tmux or Zellij detection. If opening a workspace fails, ygg reports the error and falls back to a subshell without trying another backend. If closing a workspace fails, ygg warns without undoing Git removal.

## Agent Skills

ygg includes a skill file for AI coding agents that teaches them how to use ygg for worktree management.

### Supported agents

- [Claude Code](https://github.com/anthropics/claude-code)
- [Codex](https://github.com/openai/codex)

### Install

```bash
ygg skill install
```

### Uninstall

```bash
ygg skill uninstall
```

## How it works

Ygg selects workspace ownership in this order: Herdr → ygg-shell → tmux → Zellij → subshell. Herdr uses native grouped worktree workspaces named with the full branch name; tmux and Zellij use named `<repo>/<worktree>` workspaces. Otherwise, ygg spawns a subshell in the worktree directory; when you're done, `exit` to return to where you started.

Inside a ygg shell, `ygg new` and `ygg switch` emit a `cd` instruction before tmux or Zellij detection, so the wrapper changes directory directly instead of nesting shells or opening a workspace. `ygg new` creates or focuses a workspace, while `ygg switch` focuses or reopens one. `ygg remove` and `ygg clean` close a matching workspace only after Git removes the worktree; an open failure falls back directly to a subshell, and a close failure only warns without undoing removal.

## Requirements

- Go 1.22+
- Git

## License

MIT
