# Herdr Workspace Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `ygg new`, `switch`, `remove`, and `clean` open, focus, label, and close native Herdr worktree workspaces while ygg retains ownership of every Git worktree mutation.

**Architecture:** Extend the existing backend abstraction with a structured target and a two-phase close plan. A focused `internal/herdr` adapter drives Herdr’s supported JSON CLI, opening existing ygg-created checkouts by path and resolving close targets from exact worktree provenance before Git deletion.

**Tech Stack:** Go 1.24, Cobra, standard-library `os/exec` and `encoding/json`, Git CLI, Herdr CLI 0.6.2+

## Global Constraints

- Ygg remains the only component that creates or removes Git worktrees.
- Preserve `.worktrees/<name>`, base-ref selection, copied-file behavior, dirty checks, and merge checks.
- Herdr child labels are the full branch name without a repository prefix; detached checkouts fall back to the worktree name.
- Match Herdr workspaces only by the exact absolute cleaned `worktree.checkout_path`.
- Detect Herdr only when `HERDR_ENV=1` and require `HERDR_WORKSPACE_ID` to open.
- Route Herdr, ygg-shell `cd`, tmux, Zellij, then subshell.
- Never cascade to a lower-priority backend after selecting Herdr.
- Open failures fall back to a subshell. Close failures are warnings and never roll back Git removal.
- Preserve tmux and Zellij `<repo>/<worktree>` naming.
- Require Herdr 0.6.2+ and add no Go dependency.
- Invoke commands with argument slices, never shell-built strings.

---

## File Structure

- Create `internal/herdr/herdr.go` for detection, JSON CLI calls, exact-path lookup, open/focus, and close-by-ID.
- Create `internal/herdr/herdr_test.go` for adapter unit tests with a fake runner.
- Modify `internal/multiplexer/multiplexer.go` and its test for structured targets, close plans, Herdr registration, and priority.
- Modify `internal/cli/multiplexer.go` and its test for target conversion, routing, fallback, and ordered removal.
- Modify `internal/cli/new.go` and `switch.go` to use centralized routing.
- Modify `internal/cli/remove.go` and `clean.go` to prepare close before Git removal and execute afterward.
- Modify `README.md`, CLI help, and `internal/skill/SKILL.md` for user and agent documentation.

---

### Task 1: Introduce Structured Workspace Targets

**Files:**
- Modify: `internal/multiplexer/multiplexer.go:8-53`
- Modify: `internal/multiplexer/multiplexer_test.go:41-75`
- Modify: `internal/cli/multiplexer.go:8-30`
- Modify: `internal/cli/multiplexer_test.go:9-81`
- Modify: `internal/cli/new.go:89-95`
- Modify: `internal/cli/switch.go:49-55`

**Interfaces:**
- Produces: `multiplexer.Target{Path, RepoName, WorktreeName, Branch string}`.
- Produces: `targetFor(*worktree.Worktree, string) multiplexer.Target`.
- Produces: `Backend.Open(Target) error`; Task 3 replaces the temporary positional close method.
- Consumes: existing `tmux.OpenWindow` and `zellij.OpenTab`.

- [ ] **Step 1: Write failing target tests**

Replace the open delegation test in `internal/multiplexer/multiplexer_test.go`:

```go
func TestFunctionBackendDelegatesTarget(t *testing.T) {
	wantErr := errors.New("open failed")
	want := Target{
		Path: "/repo/.worktrees/auth", RepoName: "repo",
		WorktreeName: "auth", Branch: "feat/auth",
	}
	var got Target
	backend := functionBackend{
		name: "test", activeFn: func() bool { return true },
		openFn: func(target Target) error { got = target; return wantErr },
		closeFn: func(string, string) error { return nil },
	}
	if err := backend.Open(want); !errors.Is(err, wantErr) {
		t.Fatalf("Open() error = %v, want %v", err, wantErr)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Open() target = %#v, want %#v", got, want)
	}
}
```

Add to `internal/cli/multiplexer_test.go`:

```go
func TestTargetForPreservesFullBranch(t *testing.T) {
	wt := &worktree.Worktree{
		Path: "/repo/.worktrees/feat/auth", Name: "auth", Branch: "feat/auth",
	}
	want := multiplexer.Target{
		Path: wt.Path, RepoName: "repo", WorktreeName: "auth", Branch: "feat/auth",
	}
	if got := targetFor(wt, "repo"); !reflect.DeepEqual(got, want) {
		t.Fatalf("targetFor() = %#v, want %#v", got, want)
	}
}

func TestEnterWorktreePassesStructuredTarget(t *testing.T) {
	target := multiplexer.Target{
		Path: "/repo/.worktrees/auth", RepoName: "repo",
		WorktreeName: "auth", Branch: "feat/auth",
	}
	backend := &fakeMultiplexer{name: "tmux"}
	spawned := false
	err := enterWorktreeWithSpawner(target, backend, func(string, string) error {
		spawned = true
		return nil
	})
	if err != nil {
		t.Fatalf("enterWorktreeWithSpawner() error = %v", err)
	}
	if spawned {
		t.Fatal("spawn called after successful backend open")
	}
	if !reflect.DeepEqual(backend.openTarget, target) {
		t.Fatalf("Open() target = %#v, want %#v", backend.openTarget, target)
	}
}
```

Import `multiplexer` and `worktree` in the CLI test.

- [ ] **Step 2: Verify the tests fail**

```bash
go test ./internal/multiplexer ./internal/cli -run 'TestFunctionBackendDelegatesTarget|TestTargetForPreservesFullBranch|TestEnterWorktreePassesStructuredTarget' -v
```

Expected: compilation fails because `Target`, `targetFor`, and `openTarget` do not exist.

- [ ] **Step 3: Implement the target contract**

Add in `internal/multiplexer/multiplexer.go`:

```go
type Target struct {
	Path         string
	RepoName     string
	WorktreeName string
	Branch       string
}

type Backend interface {
	Name() string
	Active() bool
	Open(Target) error
	Close(repoName, worktreeName string) error
}
```

Change `functionBackend.openFn` to `func(Target) error` and `Open` to:

```go
func (b functionBackend) Open(target Target) error {
	return b.openFn(target)
}
```

Wrap tmux and Zellij:

```go
openFn: func(target Target) error {
	return tmux.OpenWindow(target.Path, target.RepoName, target.WorktreeName)
},
```

and:

```go
openFn: func(target Target) error {
	return zellij.OpenTab(target.Path, target.RepoName, target.WorktreeName)
},
```

- [ ] **Step 4: Convert CLI open helpers**

Add the `worktree` import and:

```go
func targetFor(wt *worktree.Worktree, repoName string) multiplexer.Target {
	return multiplexer.Target{
		Path: wt.Path, RepoName: repoName,
		WorktreeName: wt.Name, Branch: wt.Branch,
	}
}
```

Change the helpers:

```go
func enterWorktree(target multiplexer.Target) error {
	return enterWorktreeWithSpawner(target, multiplexer.Detect(), shell.Spawn)
}

func enterWorktreeWithSpawner(
	target multiplexer.Target,
	backend multiplexer.Backend,
	spawn shellSpawner,
) error {
	if backend != nil {
		info("Opening %s workspace...", backend.Name())
		if err := backend.Open(target); err == nil {
			return nil
		} else {
			info("%s failed, falling back to subshell: %v", backend.Name(), err)
		}
	}
	info("Entering %s (exit to return)...", target.WorktreeName)
	return spawn(target.Path, target.WorktreeName)
}
```

Change the fake to store `openTarget multiplexer.Target`.

- [ ] **Step 5: Pass targets from new and switch**

In both commands replace the final call with:

```go
return enterWorktree(targetFor(wt, wm.RepoName()))
```

Leave the existing `InYggShell()` blocks until Task 4.

- [ ] **Step 6: Run tests**

```bash
gofmt -w internal/multiplexer/multiplexer.go internal/multiplexer/multiplexer_test.go internal/cli/multiplexer.go internal/cli/multiplexer_test.go internal/cli/new.go internal/cli/switch.go
go test ./internal/multiplexer ./internal/cli ./internal/tmux ./internal/zellij -v
```

Expected: all tests pass and tmux/Zellij still receive their original values.

- [ ] **Step 7: Commit**

```bash
git add internal/multiplexer/multiplexer.go internal/multiplexer/multiplexer_test.go internal/cli/multiplexer.go internal/cli/multiplexer_test.go internal/cli/new.go internal/cli/switch.go
git commit -m "refactor: pass structured workspace targets"
```

---

### Task 2: Build the Herdr CLI Adapter

**Files:**
- Create: `internal/herdr/herdr.go`
- Create: `internal/herdr/herdr_test.go`

**Interfaces:**
- Produces: `InHerdr() bool`.
- Produces: `WorkspaceLabel(branch, worktreeName string) string`.
- Produces: `OpenWorkspace(path, branch, worktreeName string) error`.
- Produces: `PrepareClose(path string) (string, bool, error)`.
- Produces: `CloseWorkspace(workspaceID string) error`.

- [ ] **Step 1: Write failing detection and label tests**

```go
package herdr

import "testing"

func TestInHerdr(t *testing.T) {
	for _, tt := range []struct {
		name string
		env  string
		want bool
	}{
		{name: "exact marker", env: "1", want: true},
		{name: "unset", env: "", want: false},
		{name: "other value", env: "true", want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HERDR_ENV", tt.env)
			if got := InHerdr(); got != tt.want {
				t.Fatalf("InHerdr() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWorkspaceLabel(t *testing.T) {
	if got := WorkspaceLabel("feat/auth", "auth"); got != "feat/auth" {
		t.Fatalf("WorkspaceLabel() = %q", got)
	}
	if got := WorkspaceLabel("", "detached"); got != "detached" {
		t.Fatalf("detached WorkspaceLabel() = %q", got)
	}
}
```

- [ ] **Step 2: Verify detection tests fail**

```bash
go test ./internal/herdr -run 'TestInHerdr|TestWorkspaceLabel' -v
```

Expected: compilation fails because the implementation is missing.

- [ ] **Step 3: Implement foundations**

Create `internal/herdr/herdr.go`:

```go
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
```

- [ ] **Step 4: Verify detection tests pass**

```bash
gofmt -w internal/herdr/herdr.go internal/herdr/herdr_test.go
go test ./internal/herdr -run 'TestInHerdr|TestWorkspaceLabel' -v
```

Expected: both tests pass.

- [ ] **Step 5: Add the fake runner and failing open tests**

Add a fake with queued results and exact calls:

```go
type commandCall struct {
	name string
	args []string
}
type commandResult struct {
	output string
	err    error
}
type fakeRunner struct {
	results []commandResult
	calls   []commandCall
}
func (f *fakeRunner) CombinedOutput(name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, commandCall{name: name, args: append([]string(nil), args...)})
	result := f.results[0]
	f.results = f.results[1:]
	return []byte(result.output), result.err
}
func useRunner(t *testing.T, runner commandRunner) {
	t.Helper()
	previous := commands
	commands = runner
	t.Cleanup(func() { commands = previous })
}
```

Add:

```go
func TestOpenWorkspaceUsesCallerGroupPathBranchAndFocus(t *testing.T) {
	t.Setenv("HERDR_WORKSPACE_ID", "w4")
	fake := &fakeRunner{results: []commandResult{{
		output: `{"id":"cli","result":{"type":"worktree_opened","workspace":{"workspace_id":"w5"}}}`,
	}}}
	useRunner(t, fake)
	err := OpenWorkspace("/repo/.worktrees/auth", "feat/auth", "auth")
	if err != nil {
		t.Fatalf("OpenWorkspace() error = %v", err)
	}
	want := commandCall{name: "herdr", args: []string{
		"worktree", "open", "--workspace", "w4",
		"--path", "/repo/.worktrees/auth",
		"--label", "feat/auth", "--focus",
	}}
	if !reflect.DeepEqual(fake.calls, []commandCall{want}) {
		t.Fatalf("calls = %#v, want %#v", fake.calls, []commandCall{want})
	}
}

func TestOpenWorkspaceRequiresCallerWorkspace(t *testing.T) {
	t.Setenv("HERDR_WORKSPACE_ID", "")
	fake := &fakeRunner{}
	useRunner(t, fake)
	err := OpenWorkspace("/repo/.worktrees/auth", "feat/auth", "auth")
	if err == nil || !strings.Contains(err.Error(), "HERDR_WORKSPACE_ID") {
		t.Fatalf("OpenWorkspace() error = %v", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("calls = %#v, want none", fake.calls)
	}
}
```

Import `errors`, `reflect`, and `strings`, then add the failure table:

```go
func TestOpenWorkspaceRejectsCommandAndResponseFailures(t *testing.T) {
	tests := []struct {
		name, output, wantError string
		err                     error
	}{
		{name: "command", output: "socket unavailable", err: errors.New("exit 1"), wantError: "socket unavailable"},
		{name: "malformed JSON", output: "not-json", wantError: "failed to parse"},
		{name: "wrong type", output: `{"result":{"type":"workspace_created","workspace":{"workspace_id":"w5"}}}`, wantError: "workspace_created"},
		{name: "missing ID", output: `{"result":{"type":"worktree_opened","workspace":{}}}`, wantError: "no workspace ID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HERDR_WORKSPACE_ID", "w4")
			fake := &fakeRunner{results: []commandResult{{output: tt.output, err: tt.err}}}
			useRunner(t, fake)
			err := OpenWorkspace("/repo/.worktrees/auth", "feat/auth", "auth")
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("OpenWorkspace() error = %v, want containing %q", err, tt.wantError)
			}
		})
	}
}
```

- [ ] **Step 6: Verify open tests fail**

```bash
go test ./internal/herdr -run TestOpenWorkspace -v
```

Expected: compilation fails because `OpenWorkspace` is missing.

- [ ] **Step 7: Implement open and validation**

```go
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
```

- [ ] **Step 8: Verify open tests pass**

```bash
gofmt -w internal/herdr/herdr.go internal/herdr/herdr_test.go
go test ./internal/herdr -run TestOpenWorkspace -v
```

Expected: all open cases pass.

- [ ] **Step 9: Add failing lookup and close tests**

Use workspace-list JSON with primary, exact, and prefix-only paths:

```go
func TestPrepareCloseMatchesExactCheckoutPath(t *testing.T) {
	fake := &fakeRunner{results: []commandResult{{output: `{
		"id":"cli","result":{"type":"workspace_list","workspaces":[
			{"workspace_id":"w4","worktree":null},
			{"workspace_id":"w5","worktree":{"checkout_path":"/repo/.worktrees/auth"}},
			{"workspace_id":"w6","worktree":{"checkout_path":"/repo/.worktrees/auth-extra"}}
		]}
	}`}}}
	useRunner(t, fake)
	id, found, err := PrepareClose("/repo/.worktrees/auth/.")
	if err != nil {
		t.Fatalf("PrepareClose() error = %v", err)
	}
	if id != "w5" || !found {
		t.Fatalf("PrepareClose() = (%q, %v), want (%q, true)", id, found, "w5")
	}
}
```

Add the remaining lookup cases:

```go
func TestPrepareCloseEdgeCases(t *testing.T) {
	tests := []struct {
		name, output, wantID, wantError string
		commandErr                      error
		wantFound                      bool
	}{
		{name: "no match", output: `{"result":{"type":"workspace_list","workspaces":[]}}`},
		{name: "duplicate", output: `{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"w5","worktree":{"checkout_path":"/repo/wt"}},{"workspace_id":"w6","worktree":{"checkout_path":"/repo/wt"}}]}}`, wantError: "multiple Herdr workspaces"},
		{name: "command", output: "socket unavailable", commandErr: errors.New("exit 1"), wantError: "socket unavailable"},
		{name: "malformed JSON", output: "not-json", wantError: "failed to parse"},
		{name: "wrong type", output: `{"result":{"type":"workspace_info"}}`, wantError: "workspace_info"},
		{name: "missing ID", output: `{"result":{"type":"workspace_list","workspaces":[{"worktree":{"checkout_path":"/repo/wt"}}]}}`, wantError: "no workspace ID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeRunner{results: []commandResult{{output: tt.output, err: tt.commandErr}}}
			useRunner(t, fake)
			id, found, err := PrepareClose("/repo/wt")
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("PrepareClose() error = %v, want containing %q", err, tt.wantError)
				}
				return
			}
			if err != nil || id != tt.wantID || found != tt.wantFound {
				t.Fatalf("PrepareClose() = (%q, %v, %v)", id, found, err)
			}
		})
	}
}

func TestCloseWorkspaceUsesOpaqueID(t *testing.T) {
	fake := &fakeRunner{results: []commandResult{{output: `{"result":{"type":"workspace_closed"}}`}}}
	useRunner(t, fake)
	if err := CloseWorkspace("w5"); err != nil {
		t.Fatalf("CloseWorkspace() error = %v", err)
	}
	want := []commandCall{{name: "herdr", args: []string{"workspace", "close", "w5"}}}
	if !reflect.DeepEqual(fake.calls, want) {
		t.Fatalf("calls = %#v, want %#v", fake.calls, want)
	}
}

func TestCloseWorkspaceRejectsEmptyID(t *testing.T) {
	if err := CloseWorkspace(""); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("CloseWorkspace() error = %v, want empty-ID error", err)
	}
}

func TestCloseWorkspaceReportsCommandFailure(t *testing.T) {
	fake := &fakeRunner{results: []commandResult{{output: "close denied", err: errors.New("exit 1")}}}
	useRunner(t, fake)
	if err := CloseWorkspace("w5"); err == nil || !strings.Contains(err.Error(), "close denied") {
		t.Fatalf("CloseWorkspace() error = %v, want command output", err)
	}
}

func TestCloseWorkspaceRejectsInvalidResponses(t *testing.T) {
	for _, tt := range []struct{ name, output, wantError string }{
		{name: "malformed JSON", output: "not-json", wantError: "failed to parse"},
		{name: "wrong type", output: `{"result":{"type":"workspace_info"}}`, wantError: "workspace_info"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeRunner{results: []commandResult{{output: tt.output}}}
			useRunner(t, fake)
			if err := CloseWorkspace("w5"); err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("CloseWorkspace() error = %v, want containing %q", err, tt.wantError)
			}
		})
	}
}
```

- [ ] **Step 10: Verify lookup and close tests fail**

```bash
go test ./internal/herdr -run 'TestPrepareClose|TestCloseWorkspace' -v
```

Expected: compilation fails because both functions are missing.

- [ ] **Step 11: Implement lookup and close**

```go
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
```

- [ ] **Step 12: Run all adapter tests**

```bash
gofmt -w internal/herdr/herdr.go internal/herdr/herdr_test.go
go test ./internal/herdr -v
```

Expected: all tests pass without a live server.

- [ ] **Step 13: Commit**

```bash
git add internal/herdr/herdr.go internal/herdr/herdr_test.go
git commit -m "feat: add herdr workspace adapter"
```

---

### Task 3: Add Two-Phase Workspace Closure

**Files:**
- Modify: `internal/multiplexer/multiplexer.go:8-53`
- Modify: `internal/multiplexer/multiplexer_test.go`
- Modify: `internal/cli/multiplexer.go`
- Modify: `internal/cli/multiplexer_test.go`
- Modify: `internal/cli/remove.go:33-133`
- Modify: `internal/cli/clean.go:119-129`

**Interfaces:**
- Produces: `ClosePlan`, `NewClosePlan(bool, func() error)`, `Matched() bool`, and `Execute() error`.
- Produces: `Backend.PrepareClose(Target) (ClosePlan, error)`.
- Produces: `removeWithWorkspace(Backend, Target, func() error) workspaceRemovalResult`.
- Consumes: Task 1’s `Target` and existing tmux/Zellij close functions.

- [ ] **Step 1: Write failing close-plan tests**

```go
func TestClosePlan(t *testing.T) {
	wantErr := errors.New("close failed")
	called := false
	plan := NewClosePlan(true, func() error { called = true; return wantErr })
	if !plan.Matched() {
		t.Fatal("Matched() = false, want true")
	}
	if err := plan.Execute(); !errors.Is(err, wantErr) {
		t.Fatalf("Execute() error = %v, want %v", err, wantErr)
	}
	if !called {
		t.Fatal("prepared close function was not called")
	}
}

func TestZeroClosePlanIsNoOp(t *testing.T) {
	var plan ClosePlan
	if plan.Matched() {
		t.Fatal("zero ClosePlan matched unexpectedly")
	}
	if err := plan.Execute(); err != nil {
		t.Fatalf("zero ClosePlan Execute() error = %v", err)
	}
}
```

Update the fake backend to implement `PrepareClose` and record its target.

- [ ] **Step 2: Write failing ordered-removal tests**

Add to the fake:

```go
prepareErr    error
preparePlan   multiplexer.ClosePlan
prepareTarget multiplexer.Target
order         *[]string

func (f *fakeMultiplexer) PrepareClose(target multiplexer.Target) (multiplexer.ClosePlan, error) {
	f.prepareTarget = target
	if f.order != nil { *f.order = append(*f.order, "prepare") }
	return f.preparePlan, f.prepareErr
}
```

Add:

```go
func TestRemoveWithWorkspacePreparesRemovesThenCloses(t *testing.T) {
	var order []string
	backend := &fakeMultiplexer{name: "tmux", order: &order}
	backend.preparePlan = multiplexer.NewClosePlan(true, func() error {
		order = append(order, "close")
		return nil
	})
	target := multiplexer.Target{Path: "/repo/.worktrees/auth", WorktreeName: "auth"}
	result := removeWithWorkspace(backend, target, func() error {
		order = append(order, "remove")
		return nil
	})
	if !reflect.DeepEqual(order, []string{"prepare", "remove", "close"}) {
		t.Fatalf("order = %#v", order)
	}
	if result.PrepareError != nil || result.RemoveError != nil || result.CloseError != nil {
		t.Fatalf("unexpected errors: %#v", result)
	}
	if !result.WorkspaceHandled {
		t.Fatal("WorkspaceHandled = false, want true")
	}
}
```

Add the four failure/no-backend tests:

```go
func TestRemoveWithWorkspacePreparationFailureStillRemoves(t *testing.T) {
	prepareErr := errors.New("prepare failed")
	removed := false
	closed := false
	backend := &fakeMultiplexer{name: "Herdr", prepareErr: prepareErr}
	backend.preparePlan = multiplexer.NewClosePlan(true, func() error { closed = true; return nil })
	result := removeWithWorkspace(backend, multiplexer.Target{}, func() error { removed = true; return nil })
	if !errors.Is(result.PrepareError, prepareErr) || !removed || closed {
		t.Fatalf("result = %#v, removed=%v closed=%v", result, removed, closed)
	}
}

func TestRemoveWithWorkspaceRemovalFailureSkipsClose(t *testing.T) {
	removeErr := errors.New("remove failed")
	closed := false
	backend := &fakeMultiplexer{name: "Herdr"}
	backend.preparePlan = multiplexer.NewClosePlan(true, func() error { closed = true; return nil })
	result := removeWithWorkspace(backend, multiplexer.Target{}, func() error { return removeErr })
	if !errors.Is(result.RemoveError, removeErr) || closed {
		t.Fatalf("result = %#v, closed=%v", result, closed)
	}
}

func TestRemoveWithWorkspaceCloseFailureIsNonHandled(t *testing.T) {
	closeErr := errors.New("close failed")
	backend := &fakeMultiplexer{name: "Herdr", preparePlan: multiplexer.NewClosePlan(true, func() error { return closeErr })}
	result := removeWithWorkspace(backend, multiplexer.Target{}, func() error { return nil })
	if !errors.Is(result.CloseError, closeErr) || result.WorkspaceHandled {
		t.Fatalf("result = %#v", result)
	}
}

func TestRemoveWithWorkspaceWithoutBackendOnlyRemoves(t *testing.T) {
	removed := false
	result := removeWithWorkspace(nil, multiplexer.Target{}, func() error { removed = true; return nil })
	if !removed || result.WorkspaceHandled || result.PrepareError != nil || result.RemoveError != nil || result.CloseError != nil {
		t.Fatalf("result = %#v, removed=%v", result, removed)
	}
}
```

- [ ] **Step 3: Verify close tests fail**

```bash
go test ./internal/multiplexer ./internal/cli -run 'TestClosePlan|TestZeroClosePlan|TestRemoveWithWorkspace' -v
```

Expected: compilation fails for the new contract and helper types.

- [ ] **Step 4: Implement the close plan**

```go
type ClosePlan struct {
	matched bool
	execute func() error
}

func NewClosePlan(matched bool, execute func() error) ClosePlan {
	return ClosePlan{matched: matched, execute: execute}
}
func (p ClosePlan) Matched() bool { return p.matched }
func (p ClosePlan) Execute() error {
	if p.execute == nil { return nil }
	return p.execute()
}
```

Replace `Backend.Close` with `PrepareClose(Target) (ClosePlan, error)` and change `functionBackend` accordingly.

For tmux:

```go
prepareCloseFn: func(target Target) (ClosePlan, error) {
	return NewClosePlan(false, func() error {
		return tmux.CloseWindow(target.RepoName, target.WorktreeName)
	}), nil
},
```

Use `zellij.CloseTab` in the equivalent adapter. Legacy adapters keep `matched=false` because they perform lookup during execution; their current close behavior is otherwise unchanged.

- [ ] **Step 5: Implement ordered orchestration**

Add to `internal/cli/multiplexer.go`:

```go
type workspaceRemovalResult struct {
	WorkspaceHandled bool
	PrepareError     error
	RemoveError      error
	CloseError       error
}

func removeWithWorkspace(
	backend multiplexer.Backend,
	target multiplexer.Target,
	remove func() error,
) workspaceRemovalResult {
	var result workspaceRemovalResult
	var plan multiplexer.ClosePlan
	if backend != nil {
		plan, result.PrepareError = backend.PrepareClose(target)
	}
	result.RemoveError = remove()
	if result.RemoveError != nil || result.PrepareError != nil {
		return result
	}
	result.CloseError = plan.Execute()
	result.WorkspaceHandled = result.CloseError == nil && plan.Matched()
	return result
}
```

- [ ] **Step 6: Refactor remove**

Retain `var target *worktree.Worktree` instead of only its name. Assign it from `wm.Get` or `wm.Current`, preserve every safety check, and determine current removal with:

```go
needsCd = current != nil && current.Path == target.Path
```

After checks:

```go
result := removeWithWorkspace(
	multiplexer.Detect(),
	targetFor(target, wm.RepoName()),
	func() error { return wm.Remove(target.Name) },
)
if result.PrepareError != nil {
	info("Could not prepare workspace close: %v", result.PrepareError)
}
if result.RemoveError != nil {
	errorMsg("Failed to remove worktree: %v", result.RemoveError)
	return result.RemoveError
}
success("Removed worktree: %s", target.Name)
if result.CloseError != nil {
	info("Could not close workspace: %v", result.CloseError)
}
if needsCd && result.WorkspaceHandled {
	return nil
}
```

Keep the existing change-to-primary and fallback-shell path for unmatched or failed closure.

- [ ] **Step 7: Refactor clean**

After dry-run and confirmation, detect once. For each worktree:

```go
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
```

- [ ] **Step 8: Run tests**

```bash
gofmt -w internal/multiplexer/multiplexer.go internal/multiplexer/multiplexer_test.go internal/cli/multiplexer.go internal/cli/multiplexer_test.go internal/cli/remove.go internal/cli/clean.go
go test ./internal/multiplexer ./internal/cli ./internal/tmux ./internal/zellij -v
```

Expected: all tests pass and removal ordering is proven.

- [ ] **Step 9: Commit**

```bash
git add internal/multiplexer/multiplexer.go internal/multiplexer/multiplexer_test.go internal/cli/multiplexer.go internal/cli/multiplexer_test.go internal/cli/remove.go internal/cli/clean.go
git commit -m "refactor: prepare workspace closure before removal"
```

---

### Task 4: Register Herdr and Apply Routing Precedence

**Files:**
- Modify: `internal/multiplexer/multiplexer.go`
- Modify: `internal/multiplexer/multiplexer_test.go`
- Modify: `internal/cli/multiplexer.go`
- Modify: `internal/cli/multiplexer_test.go`
- Modify: `internal/cli/new.go:89-95`
- Modify: `internal/cli/switch.go:49-55`

**Interfaces:**
- Consumes: all Task 2 Herdr functions and Task 3 close plans.
- Produces: Herdr-first `Detect()` and Herdr-over-ygg-shell routing.

- [ ] **Step 1: Write failing selection tests**

Use:

```go
tests := []struct {
	name, herdr, tmux, zellij, want string
}{
	{name: "Herdr only", herdr: "1", want: "Herdr"},
	{name: "tmux only", tmux: "set", want: "tmux"},
	{name: "Zellij only", zellij: "set", want: "Zellij"},
	{name: "Herdr wins all markers", herdr: "1", tmux: "set", zellij: "set", want: "Herdr"},
	{name: "tmux wins nested Zellij", tmux: "set", zellij: "set", want: "tmux"},
	{name: "noncanonical Herdr ignored", herdr: "true", tmux: "set", want: "tmux"},
	{name: "none"},
}
```

Set all three variables in every subtest.

- [ ] **Step 2: Write failing ygg-shell routing tests**

```go
func TestUseYggShell(t *testing.T) {
	tests := []struct {
		name, yggShell string
		backend        multiplexer.Backend
		want           bool
	}{
		{name: "plain", yggShell: "1", want: true},
		{name: "tmux", yggShell: "1", backend: &fakeMultiplexer{name: "tmux"}, want: true},
		{name: "Zellij", yggShell: "1", backend: &fakeMultiplexer{name: "Zellij"}, want: true},
		{name: "Herdr override", yggShell: "1", backend: &fakeMultiplexer{name: "Herdr"}, want: false},
		{name: "not ygg shell", backend: &fakeMultiplexer{name: "Herdr"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("YGG_SHELL", tt.yggShell)
			if got := useYggShell(tt.backend); got != tt.want {
				t.Fatalf("useYggShell() = %v, want %v", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 3: Verify tests fail**

```bash
go test ./internal/multiplexer ./internal/cli -run 'TestDetect|TestUseYggShell' -v
```

Expected: Herdr selection fails and `useYggShell` is missing.

- [ ] **Step 4: Register Herdr first**

Import `internal/herdr` and prepend:

```go
functionBackend{
	name: "Herdr", activeFn: herdr.InHerdr,
	openFn: func(target Target) error {
		return herdr.OpenWorkspace(target.Path, target.Branch, target.WorktreeName)
	},
	prepareCloseFn: func(target Target) (ClosePlan, error) {
		workspaceID, found, err := herdr.PrepareClose(target.Path)
		if err != nil { return ClosePlan{}, err }
		if !found { return ClosePlan{}, nil }
		return NewClosePlan(true, func() error {
			return herdr.CloseWorkspace(workspaceID)
		}), nil
	},
},
```

Keep tmux second and Zellij third.

- [ ] **Step 5: Centralize routing**

Add `fmt` and:

```go
func useYggShell(backend multiplexer.Backend) bool {
	return InYggShell() && (backend == nil || backend.Name() != "Herdr")
}

func enterWorktree(target multiplexer.Target) error {
	backend := multiplexer.Detect()
	if useYggShell(backend) {
		fmt.Printf("cd %s\n", target.Path)
		return nil
	}
	return enterWorktreeWithSpawner(target, backend, shell.Spawn)
}
```

Delete the early `InYggShell()` blocks from `new.go` and `switch.go` and remove unused `fmt` imports. Both end with `enterWorktree(targetFor(...))`.

- [ ] **Step 6: Run tests**

```bash
gofmt -w internal/multiplexer/multiplexer.go internal/multiplexer/multiplexer_test.go internal/cli/multiplexer.go internal/cli/multiplexer_test.go internal/cli/new.go internal/cli/switch.go
go test ./internal/herdr ./internal/multiplexer ./internal/cli -v
go test ./...
```

Expected: Herdr wins all nested cases, ygg-shell still precedes tmux/Zellij, and all tests pass.

- [ ] **Step 7: Commit**

```bash
git add internal/multiplexer/multiplexer.go internal/multiplexer/multiplexer_test.go internal/cli/multiplexer.go internal/cli/multiplexer_test.go internal/cli/new.go internal/cli/switch.go
git commit -m "feat: route worktrees through herdr"
```

---

### Task 5: Document Herdr Behavior

**Files:**
- Create: `internal/cli/documentation_test.go`
- Modify: `internal/cli/new.go:14-25`
- Modify: `internal/cli/switch.go:14-18`
- Modify: `README.md:35-43,57-61,116-126,148-152`
- Modify: `internal/skill/SKILL.md:12-44`

**Interfaces:**
- Consumes: completed Tasks 1-4.
- Produces: exact user and agent documentation.

- [ ] **Step 1: Add a failing help contract**

```go
package cli

import (
	"strings"
	"testing"
)

func TestWorkspaceHelpMentionsHerdr(t *testing.T) {
	for name, help := range map[string]string{
		"new": newCmd.Long, "switch": switchCmd.Long,
	} {
		if !strings.Contains(help, "Herdr") {
			t.Errorf("%s help does not mention Herdr", name)
		}
	}
}
```

- [ ] **Step 2: Verify it fails**

```bash
go test ./internal/cli -run TestWorkspaceHelpMentionsHerdr -v
```

Expected: both help strings fail.

- [ ] **Step 3: Update command help**

Use this `new` step:

```text
3. Open the worktree in the active Herdr/tmux/Zellij workspace manager, or
   enter a subshell when no workspace backend is active
```

Use this `switch` paragraph:

```text
Inside Herdr, tmux, or Zellij, this focuses an existing named workspace or
creates one. Otherwise, it spawns a subshell in the worktree directory; exit
that shell to return to your original directory.
```

- [ ] **Step 4: Update README**

State:

- `HERDR_ENV=1` detection, native grouped worktree workspaces, and Herdr 0.6.2+.
- Exact full-branch labels such as `xyz` and `feat/auth`.
- Unchanged tmux/Zellij `<repo>/<worktree>` names.
- `new` creates/focuses, `switch` focuses/reopens, and `remove`/`clean` close only after removal.
- Herdr → ygg-shell → tmux → Zellij → subshell.
- Open failure reports and falls back to a subshell without trying another backend.
- Close failure warns without undoing Git removal.

Update “How it works” with the same ownership and precedence rules.

- [ ] **Step 5: Update the bundled skill**

Replace the workspace bullets with:

```markdown
- Herdr is detected via `HERDR_ENV=1`; ygg opens or focuses a native worktree workspace labeled with the full branch name
- Herdr takes precedence over ygg-shell, tmux, and Zellij; without Herdr, ygg-shell keeps its existing `cd` behavior before tmux/Zellij detection
- Tmux and Zellij keep named `<repo>/<worktree>` workspaces, with tmux preceding Zellij
- `ygg remove` and `ygg clean` close matching workspaces only after successful worktree removal
```

- [ ] **Step 6: Run documentation checks**

```bash
gofmt -w internal/cli/new.go internal/cli/switch.go internal/cli/documentation_test.go
go test ./internal/cli -run TestWorkspaceHelpMentionsHerdr -v
go test ./...
git diff --check
```

Expected: all tests pass and `git diff --check` prints nothing.

- [ ] **Step 7: Commit**

```bash
git add README.md internal/skill/SKILL.md internal/cli/new.go internal/cli/switch.go internal/cli/documentation_test.go
git commit -m "docs: document herdr workspace support"
```

---

### Task 6: Verify the Complete Integration

**Files:**
- Verify only. If a check exposes a defect, return to that task’s red/green cycle and commit the correction separately.

**Interfaces:**
- Consumes: Tasks 1-5.
- Produces: automated evidence and a disposable live Herdr lifecycle result.

- [ ] **Step 1: Run automated verification**

```bash
gofmt -l .
go vet ./...
go test ./...
git diff --check
go build -o /tmp/ygg-herdr-smoke-bin ./cmd/ygg
```

Expected: formatting and diff checks print nothing; vet, tests, and build exit zero.

- [ ] **Step 2: Check live prerequisites**

```bash
test "${HERDR_ENV:-}" = 1
command -v herdr
command -v jq
herdr --version
herdr workspace get "$HERDR_WORKSPACE_ID"
```

Expected: Herdr is 0.6.2+, both tools resolve, and caller workspace JSON is valid. Skip only the live smoke if these prerequisites fail.

- [ ] **Step 3: Create disposable Git and Herdr state**

```bash
original_workspace_id="$HERDR_WORKSPACE_ID"
smoke_root="$(mktemp -d /tmp/ygg-herdr-smoke.XXXXXX)"
smoke_repo="$smoke_root/repo"
git init -b main "$smoke_repo"
git -C "$smoke_repo" config user.name ygg-smoke
git -C "$smoke_repo" config user.email ygg-smoke@example.invalid
git -C "$smoke_repo" commit --allow-empty -m initial
parent_json="$(herdr workspace create --cwd "$smoke_repo" --label ygg-smoke-parent --no-focus)"
parent_workspace_id="$(printf '%s' "$parent_json" | jq -er '.result.workspace.workspace_id')"
```

Expected: a local `main` commit and a non-empty parent workspace ID. Record all three variables for cleanup.

- [ ] **Step 4: Verify new, full labels, and switch idempotence**

```bash
(cd "$smoke_repo" && HERDR_ENV=1 HERDR_WORKSPACE_ID="$parent_workspace_id" /tmp/ygg-herdr-smoke-bin new xyz)
xyz_path="$smoke_repo/.worktrees/xyz"
xyz_workspace_id="$(herdr workspace list | jq -er --arg path "$xyz_path" '.result.workspaces[] | select(.worktree.checkout_path == $path) | select(.label == "xyz") | .workspace_id')"
test -d "$xyz_path"
test -n "$xyz_workspace_id"

(cd "$smoke_repo" && HERDR_ENV=1 HERDR_WORKSPACE_ID="$parent_workspace_id" /tmp/ygg-herdr-smoke-bin new feat/auth)
auth_path="$smoke_repo/.worktrees/feat/auth"
auth_workspace_id="$(herdr workspace list | jq -er --arg path "$auth_path" '.result.workspaces[] | select(.worktree.checkout_path == $path) | select(.label == "feat/auth") | .workspace_id')"
test -d "$auth_path"
test -n "$auth_workspace_id"

before_count="$(herdr workspace list | jq --arg path "$xyz_path" '[.result.workspaces[] | select(.worktree.checkout_path == $path)] | length')"
(cd "$smoke_repo" && HERDR_ENV=1 HERDR_WORKSPACE_ID="$parent_workspace_id" /tmp/ygg-herdr-smoke-bin switch xyz)
after_count="$(herdr workspace list | jq --arg path "$xyz_path" '[.result.workspaces[] | select(.worktree.checkout_path == $path)] | length')"
test "$before_count" = 1
test "$after_count" = 1
```

Expected: both paths exist, labels are exact, and switch leaves one `xyz` workspace.

- [ ] **Step 5: Verify reopen and remove**

```bash
herdr workspace close "$xyz_workspace_id"
(cd "$smoke_repo" && HERDR_ENV=1 HERDR_WORKSPACE_ID="$parent_workspace_id" /tmp/ygg-herdr-smoke-bin switch xyz)
test -n "$(herdr workspace list | jq -er --arg path "$xyz_path" '.result.workspaces[] | select(.worktree.checkout_path == $path) | .workspace_id')"
(cd "$smoke_repo" && HERDR_ENV=1 HERDR_WORKSPACE_ID="$parent_workspace_id" /tmp/ygg-herdr-smoke-bin remove --force xyz)
test ! -e "$xyz_path"
test "$(herdr workspace list | jq --arg path "$xyz_path" '[.result.workspaces[] | select(.worktree.checkout_path == $path)] | length')" = 0
```

Expected: switch reopens and remove deletes the checkout plus exact workspace.

- [ ] **Step 6: Verify clean and parent preservation**

```bash
(cd "$smoke_repo" && HERDR_ENV=1 HERDR_WORKSPACE_ID="$parent_workspace_id" /tmp/ygg-herdr-smoke-bin new clean-me)
clean_path="$smoke_repo/.worktrees/clean-me"
test -n "$(herdr workspace list | jq -er --arg path "$clean_path" '.result.workspaces[] | select(.worktree.checkout_path == $path) | .workspace_id')"
(cd "$smoke_repo" && HERDR_ENV=1 HERDR_WORKSPACE_ID="$parent_workspace_id" /tmp/ygg-herdr-smoke-bin clean --force)
test ! -e "$clean_path"
test "$(herdr workspace list | jq --arg path "$clean_path" '[.result.workspaces[] | select(.worktree.checkout_path == $path)] | length')" = 0
herdr workspace get "$parent_workspace_id"
```

Expected: the merged child is removed and closed while the parent remains. `feat/auth` may also be cleaned because it has no unique commits; otherwise remove it with `remove --force feat/auth`.

- [ ] **Step 7: Restore focus and remove only recorded disposable state**

```bash
herdr workspace list | jq -r --arg prefix "$smoke_repo/.worktrees/" '.result.workspaces[] | select(.worktree.checkout_path? | startswith($prefix)) | .workspace_id' | while IFS= read -r workspace_id; do
  test -n "$workspace_id" && herdr workspace close "$workspace_id"
done
herdr workspace close "$parent_workspace_id"
herdr workspace focus "$original_workspace_id"
test -n "$smoke_root"
case "$smoke_root" in
  /tmp/ygg-herdr-smoke.*) rm -rf -- "$smoke_root" ;;
  *) printf 'refusing unexpected smoke path: %s\n' "$smoke_root" >&2; exit 1 ;;
esac
```

Expected: only recorded smoke state is removed and focus returns to the original workspace.

- [ ] **Step 8: Record final state**

```bash
git status --short --branch
git log -5 --oneline --decorate
```

Expected: no uncommitted implementation changes; task commits follow the design commit.
