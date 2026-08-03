# Tmux and Multiplexer Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add automatic, full-lifecycle tmux window support while preserving existing Zellij and subshell behavior.

**Architecture:** Add a tested `internal/tmux` command wrapper and an `internal/multiplexer` backend selector ordered as tmux, Zellij, then no backend. Route CLI entry and cleanup through focused helpers so all four commands share selection, fallback, and error behavior while `internal/zellij` remains unchanged.

**Tech Stack:** Go 1.24 module, standard library `os`, `os/exec`, and `strings`, Cobra CLI, existing `go test`/race/vet toolchain, no new dependencies.

## Global Constraints

- Tmux activation requires a non-empty `TMUX`; Zellij activation requires a non-empty `ZELLIJ`.
- Selection order is tmux, then Zellij, then the existing subshell behavior.
- `YGG_SHELL` handling remains ahead of multiplexer detection in `new` and `switch`.
- Tmux operations affect only the current session and target unique matching windows by stable window ID.
- Both backends name workspaces `<repo>/<worktree>`.
- An open failure falls back to `shell.Spawn`; a close failure is reported and remains non-fatal.
- A missing window/tab on close is a successful no-op; duplicate tmux names are an error.
- Do not modify `internal/zellij/zellij.go` or change its command behavior.
- Do not add configuration, setup commands, flags, session management, or dependencies.

## File Structure

- Create `internal/tmux/tmux.go`: tmux detection, naming, current-session lookup, and window lifecycle commands.
- Create `internal/tmux/tmux_test.go`: fake command runner plus detection, lookup, lifecycle, and error tests.
- Create `internal/multiplexer/multiplexer.go`: backend interface, function adapter, built-in ordering, and active-backend selection.
- Create `internal/multiplexer/multiplexer_test.go`: precedence, no-backend, and adapter-delegation tests.
- Create `internal/cli/multiplexer.go`: shared open-or-spawn and close helpers.
- Create `internal/cli/multiplexer_test.go`: CLI helper tests with fake backend and shell spawner.
- Modify `internal/cli/new.go`: retain `YGG_SHELL` priority and call the shared entry helper.
- Modify `internal/cli/switch.go`: retain `YGG_SHELL` priority and call the shared entry helper.
- Modify `internal/cli/remove.go`: close the selected backend after successful removal.
- Modify `internal/cli/clean.go`: detect once and close after each successful removal.
- Modify `README.md`: describe tmux and Zellij lifecycle behavior and precedence.
- Modify `internal/skill/SKILL.md`: teach agents the automatic multiplexer behavior.

---

### Task 1: Tmux Detection and Exact Window Lookup

**Files:**
- Create: `internal/tmux/tmux.go`
- Create: `internal/tmux/tmux_test.go`

**Interfaces:**
- Consumes: `TMUX` from the process environment and `tmux list-windows -F <format>` output.
- Produces: `InTmux() bool`, `WindowName(repoName, worktreeName string) string`, and private `findWindowID(name string) (id string, found bool, err error)` for Task 2.
- Produces: private `commandRunner`, `execRunner`, `commands`, `commandCall`, `fakeRunner`, `useRunner`, and `assertCalls` test seams used by Task 2.

- [ ] **Step 1: Record the clean baseline**

Run:

```bash
go test ./...
```

Expected: PASS before feature code is added.

- [ ] **Step 2: Write the failing lookup tests**

Create `internal/tmux/tmux_test.go`:

```go
package tmux

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

type commandCall struct {
	method string
	name   string
	args   []string
}

type fakeRunner struct {
	output    []byte
	outputErr error
	runErr    error
	calls     []commandCall
}

func (f *fakeRunner) Output(name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, commandCall{method: "output", name: name, args: append([]string(nil), args...)})
	return f.output, f.outputErr
}

func (f *fakeRunner) Run(name string, args ...string) error {
	f.calls = append(f.calls, commandCall{method: "run", name: name, args: append([]string(nil), args...)})
	return f.runErr
}

func useRunner(t *testing.T, runner commandRunner) {
	t.Helper()
	previous := commands
	commands = runner
	t.Cleanup(func() { commands = previous })
}

func assertCalls(t *testing.T, got, want []commandCall) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %#v, want %#v", got, want)
	}
}

func TestInTmux(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		t.Setenv("TMUX", "/tmp/tmux-1000/default,123,0")
		if !InTmux() {
			t.Fatal("InTmux() = false, want true")
		}
	})

	t.Run("unset", func(t *testing.T) {
		t.Setenv("TMUX", "")
		if InTmux() {
			t.Fatal("InTmux() = true, want false")
		}
	})
}

func TestWindowName(t *testing.T) {
	tests := []struct {
		repoName     string
		worktreeName string
		want         string
	}{
		{repoName: "ygg", worktreeName: "my-feature", want: "ygg/my-feature"},
		{repoName: "my-repo", worktreeName: "fix-bug", want: "my-repo/fix-bug"},
	}

	for _, tt := range tests {
		if got := WindowName(tt.repoName, tt.worktreeName); got != tt.want {
			t.Errorf("WindowName(%q, %q) = %q, want %q", tt.repoName, tt.worktreeName, got, tt.want)
		}
	}
}

func TestFindWindowID(t *testing.T) {
	listErr := errors.New("list failed")
	tests := []struct {
		name      string
		output    string
		outputErr error
		wantID    string
		wantFound bool
		wantError string
	}{
		{
			name:      "exact match",
			output:    "%1\tother\n%2\tygg/feature\n%3\tygg/feature-extra\n",
			wantID:    "%2",
			wantFound: true,
		},
		{name: "missing", output: "%1\tother\n"},
		{
			name:      "duplicate",
			output:    "%1\tygg/feature\n%2\tygg/feature\n",
			wantError: "multiple tmux windows named \"ygg/feature\"",
		},
		{
			name:      "malformed record",
			output:    "%1-without-a-tab\n",
			wantError: "unexpected tmux window record",
		},
		{
			name:      "list failure",
			outputErr: listErr,
			wantError: "failed to list tmux windows for \"ygg/feature\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeRunner{output: []byte(tt.output), outputErr: tt.outputErr}
			useRunner(t, fake)

			gotID, gotFound, err := findWindowID("ygg/feature")
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("findWindowID() error = %v, want containing %q", err, tt.wantError)
				}
			} else if err != nil {
				t.Fatalf("findWindowID() error = %v", err)
			}
			if gotID != tt.wantID || gotFound != tt.wantFound {
				t.Fatalf("findWindowID() = (%q, %v), want (%q, %v)", gotID, gotFound, tt.wantID, tt.wantFound)
			}

			assertCalls(t, fake.calls, []commandCall{{
				method: "output",
				name:   "tmux",
				args:   []string{"list-windows", "-F", "#{window_id}\t#{window_name}"},
			}})
		})
	}
}
```

- [ ] **Step 3: Run the new tests to verify they fail**

Run:

```bash
go test ./internal/tmux -run 'Test(InTmux|WindowName|FindWindowID)$' -v
```

Expected: FAIL because the package implementation and referenced identifiers do not exist.

- [ ] **Step 4: Implement detection, naming, runner injection, and exact lookup**

Create `internal/tmux/tmux.go`:

```go
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
```

- [ ] **Step 5: Run and format the package tests**

Run:

```bash
gofmt -w internal/tmux/tmux.go internal/tmux/tmux_test.go
go test ./internal/tmux -v
```

Expected: PASS.

- [ ] **Step 6: Commit the lookup unit**

```bash
git add internal/tmux/tmux.go internal/tmux/tmux_test.go
git commit -m "feat: add tmux window discovery"
```

---

### Task 2: Tmux Window Lifecycle

**Files:**
- Modify: `internal/tmux/tmux.go`
- Modify: `internal/tmux/tmux_test.go`

**Interfaces:**
- Consumes: `WindowName` and `findWindowID` from Task 1.
- Produces: `OpenWindow(dir, repoName, worktreeName string) error` and `CloseWindow(repoName, worktreeName string) error` for the generic adapter in Task 3.

- [ ] **Step 1: Write failing open and close tests**

Append to `internal/tmux/tmux_test.go`:

```go
func TestOpenWindowSelectsExistingWindowByID(t *testing.T) {
	fake := &fakeRunner{output: []byte("%7\tygg/feature\n")}
	useRunner(t, fake)

	if err := OpenWindow("/repo/.worktrees/feature", "ygg", "feature"); err != nil {
		t.Fatalf("OpenWindow() error = %v", err)
	}

	assertCalls(t, fake.calls, []commandCall{
		{method: "output", name: "tmux", args: []string{"list-windows", "-F", "#{window_id}\t#{window_name}"}},
		{method: "run", name: "tmux", args: []string{"select-window", "-t", "%7"}},
	})
}

func TestOpenWindowCreatesMissingWindowInWorktree(t *testing.T) {
	fake := &fakeRunner{}
	useRunner(t, fake)

	if err := OpenWindow("/repo/.worktrees/feature", "ygg", "feature"); err != nil {
		t.Fatalf("OpenWindow() error = %v", err)
	}

	assertCalls(t, fake.calls, []commandCall{
		{method: "output", name: "tmux", args: []string{"list-windows", "-F", "#{window_id}\t#{window_name}"}},
		{method: "run", name: "tmux", args: []string{"new-window", "-n", "ygg/feature", "-c", "/repo/.worktrees/feature"}},
	})
}

func TestOpenWindowReportsOperationFailure(t *testing.T) {
	tests := []struct {
		name      string
		output    string
		wantError string
	}{
		{name: "select", output: "%7\tygg/feature\n", wantError: "failed to select tmux window \"ygg/feature\""},
		{name: "create", wantError: "failed to create tmux window \"ygg/feature\""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeRunner{output: []byte(tt.output), runErr: errors.New("tmux failed")}
			useRunner(t, fake)

			err := OpenWindow("/repo/.worktrees/feature", "ygg", "feature")
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("OpenWindow() error = %v, want containing %q", err, tt.wantError)
			}
		})
	}
}

func TestCloseWindowKillsExistingWindowByID(t *testing.T) {
	fake := &fakeRunner{output: []byte("%7\tygg/feature\n")}
	useRunner(t, fake)

	if err := CloseWindow("ygg", "feature"); err != nil {
		t.Fatalf("CloseWindow() error = %v", err)
	}

	assertCalls(t, fake.calls, []commandCall{
		{method: "output", name: "tmux", args: []string{"list-windows", "-F", "#{window_id}\t#{window_name}"}},
		{method: "run", name: "tmux", args: []string{"kill-window", "-t", "%7"}},
	})
}

func TestCloseWindowMissingIsNoOp(t *testing.T) {
	fake := &fakeRunner{output: []byte("%1\tother\n")}
	useRunner(t, fake)

	if err := CloseWindow("ygg", "feature"); err != nil {
		t.Fatalf("CloseWindow() error = %v", err)
	}

	assertCalls(t, fake.calls, []commandCall{{
		method: "output",
		name:   "tmux",
		args:   []string{"list-windows", "-F", "#{window_id}\t#{window_name}"},
	}})
}

func TestCloseWindowRejectsDuplicateNames(t *testing.T) {
	fake := &fakeRunner{output: []byte("%7\tygg/feature\n%8\tygg/feature\n")}
	useRunner(t, fake)

	err := CloseWindow("ygg", "feature")
	if err == nil || !strings.Contains(err.Error(), "multiple tmux windows named \"ygg/feature\"") {
		t.Fatalf("CloseWindow() error = %v, want duplicate-name error", err)
	}
	if len(fake.calls) != 1 || fake.calls[0].method != "output" {
		t.Fatalf("duplicate lookup calls = %#v, want only list-windows", fake.calls)
	}
}

func TestCloseWindowReportsKillFailure(t *testing.T) {
	fake := &fakeRunner{
		output: []byte("%7\tygg/feature\n"),
		runErr: errors.New("kill failed"),
	}
	useRunner(t, fake)

	err := CloseWindow("ygg", "feature")
	if err == nil || !strings.Contains(err.Error(), "failed to close tmux window \"ygg/feature\"") {
		t.Fatalf("CloseWindow() error = %v, want close error", err)
	}
}
```

- [ ] **Step 2: Run the lifecycle tests to verify they fail**

Run:

```bash
go test ./internal/tmux -run 'Test(OpenWindow|CloseWindow)' -v
```

Expected: FAIL because `OpenWindow` and `CloseWindow` are undefined.

- [ ] **Step 3: Implement the lifecycle operations**

Append to `internal/tmux/tmux.go`:

```go
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
```

- [ ] **Step 4: Run the tmux package tests**

Run:

```bash
gofmt -w internal/tmux/tmux.go internal/tmux/tmux_test.go
go test ./internal/tmux -v
```

Expected: PASS, including duplicate-name and failure cases.

- [ ] **Step 5: Commit the lifecycle unit**

```bash
git add internal/tmux/tmux.go internal/tmux/tmux_test.go
git commit -m "feat: manage tmux worktree windows"
```

---

### Task 3: Generic Multiplexer Backend Selection

**Files:**
- Create: `internal/multiplexer/multiplexer.go`
- Create: `internal/multiplexer/multiplexer_test.go`
- Verify unchanged: `internal/zellij/zellij.go`

**Interfaces:**
- Consumes: `tmux.InTmux`, `tmux.OpenWindow`, `tmux.CloseWindow`, `zellij.InZellij`, `zellij.OpenTab`, and `zellij.CloseTab`.
- Produces: `Backend` with `Name`, `Active`, `Open`, and `Close`; `Detect() Backend` for CLI consumers in Task 4.

- [ ] **Step 1: Write failing selector and adapter tests**

Create `internal/multiplexer/multiplexer_test.go`:

```go
package multiplexer

import (
	"errors"
	"reflect"
	"testing"
)

func TestDetect(t *testing.T) {
	tests := []struct {
		name   string
		tmux   string
		zellij string
		want   string
	}{
		{name: "tmux only", tmux: "set", want: "tmux"},
		{name: "zellij only", zellij: "set", want: "zellij"},
		{name: "tmux wins when nested", tmux: "set", zellij: "set", want: "tmux"},
		{name: "neither"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TMUX", tt.tmux)
			t.Setenv("ZELLIJ", tt.zellij)

			got := Detect()
			if tt.want == "" {
				if got != nil {
					t.Fatalf("Detect() = %q, want nil", got.Name())
				}
				return
			}
			if got == nil || got.Name() != tt.want {
				t.Fatalf("Detect() = %v, want backend %q", got, tt.want)
			}
		})
	}
}

func TestFunctionBackendDelegates(t *testing.T) {
	wantOpenErr := errors.New("open failed")
	wantCloseErr := errors.New("close failed")
	var gotOpen []string
	var gotClose []string

	backend := functionBackend{
		name:     "test",
		activeFn: func() bool { return true },
		openFn: func(dir, repoName, worktreeName string) error {
			gotOpen = []string{dir, repoName, worktreeName}
			return wantOpenErr
		},
		closeFn: func(repoName, worktreeName string) error {
			gotClose = []string{repoName, worktreeName}
			return wantCloseErr
		},
	}

	if backend.Name() != "test" || !backend.Active() {
		t.Fatalf("backend identity/detection was not delegated")
	}
	if err := backend.Open("/repo/wt", "repo", "wt"); !errors.Is(err, wantOpenErr) {
		t.Fatalf("Open() error = %v, want %v", err, wantOpenErr)
	}
	if err := backend.Close("repo", "wt"); !errors.Is(err, wantCloseErr) {
		t.Fatalf("Close() error = %v, want %v", err, wantCloseErr)
	}
	if !reflect.DeepEqual(gotOpen, []string{"/repo/wt", "repo", "wt"}) {
		t.Fatalf("Open() args = %#v", gotOpen)
	}
	if !reflect.DeepEqual(gotClose, []string{"repo", "wt"}) {
		t.Fatalf("Close() args = %#v", gotClose)
	}
}
```

- [ ] **Step 2: Run the selector tests to verify they fail**

Run:

```bash
go test ./internal/multiplexer -v
```

Expected: FAIL because the multiplexer package implementation does not exist.

- [ ] **Step 3: Implement the interface, adapters, and ordered selector**

Create `internal/multiplexer/multiplexer.go`:

```go
package multiplexer

import (
	"github.com/joch/ygg/internal/tmux"
	"github.com/joch/ygg/internal/zellij"
)

// Backend manages worktree workspaces in one terminal multiplexer.
type Backend interface {
	Name() string
	Active() bool
	Open(dir, repoName, worktreeName string) error
	Close(repoName, worktreeName string) error
}

type functionBackend struct {
	name     string
	activeFn func() bool
	openFn   func(string, string, string) error
	closeFn  func(string, string) error
}

func (b functionBackend) Name() string {
	return b.name
}

func (b functionBackend) Active() bool {
	return b.activeFn()
}

func (b functionBackend) Open(dir, repoName, worktreeName string) error {
	return b.openFn(dir, repoName, worktreeName)
}

func (b functionBackend) Close(repoName, worktreeName string) error {
	return b.closeFn(repoName, worktreeName)
}

func builtInBackends() []Backend {
	return []Backend{
		functionBackend{
			name:     "tmux",
			activeFn: tmux.InTmux,
			openFn:   tmux.OpenWindow,
			closeFn:  tmux.CloseWindow,
		},
		functionBackend{
			name:     "zellij",
			activeFn: zellij.InZellij,
			openFn:   zellij.OpenTab,
			closeFn:  zellij.CloseTab,
		},
	}
}

// Detect returns the highest-priority active backend, or nil.
func Detect() Backend {
	for _, backend := range builtInBackends() {
		if backend.Active() {
			return backend
		}
	}
	return nil
}
```

- [ ] **Step 4: Run selector and Zellij regression tests**

Run:

```bash
gofmt -w internal/multiplexer/multiplexer.go internal/multiplexer/multiplexer_test.go
go test ./internal/multiplexer ./internal/zellij -v
git status --short internal/zellij
```

Expected: both packages PASS and `git status` prints nothing for `internal/zellij`.

- [ ] **Step 5: Commit the generic selection layer**

```bash
git add internal/multiplexer/multiplexer.go internal/multiplexer/multiplexer_test.go
git commit -m "feat: select active terminal multiplexer"
```

---

### Task 4: Route CLI Lifecycle Through the Generic Backend

**Files:**
- Create: `internal/cli/multiplexer.go`
- Create: `internal/cli/multiplexer_test.go`
- Modify: `internal/cli/new.go:7-10,95-106`
- Modify: `internal/cli/switch.go:7-10,56-67`
- Modify: `internal/cli/remove.go:7-10,114-119`
- Modify: `internal/cli/clean.go:9-11,119-130`

**Interfaces:**
- Consumes: `multiplexer.Backend`, `multiplexer.Detect() Backend`, and `shell.Spawn(dir, worktreeName string) error`.
- Produces: private `enterWorktree`, `enterWorktreeWithSpawner`, and `closeWorkspace` helpers shared by the four commands.

- [ ] **Step 1: Write failing CLI helper tests**

Create `internal/cli/multiplexer_test.go`:

```go
package cli

import (
	"errors"
	"reflect"
	"testing"
)

type fakeMultiplexer struct {
	name      string
	openErr   error
	closeErr  error
	openArgs  []string
	closeArgs []string
}

func (f *fakeMultiplexer) Name() string { return f.name }
func (f *fakeMultiplexer) Active() bool { return true }

func (f *fakeMultiplexer) Open(dir, repoName, worktreeName string) error {
	f.openArgs = []string{dir, repoName, worktreeName}
	return f.openErr
}

func (f *fakeMultiplexer) Close(repoName, worktreeName string) error {
	f.closeArgs = []string{repoName, worktreeName}
	return f.closeErr
}

func TestEnterWorktreeUsesActiveMultiplexer(t *testing.T) {
	backend := &fakeMultiplexer{name: "tmux"}
	spawned := false
	spawn := func(string, string) error {
		spawned = true
		return nil
	}

	if err := enterWorktreeWithSpawner("/repo/wt", "repo", "wt", backend, spawn); err != nil {
		t.Fatalf("enterWorktreeWithSpawner() error = %v", err)
	}
	if spawned {
		t.Fatal("spawn called after successful backend open")
	}
	if !reflect.DeepEqual(backend.openArgs, []string{"/repo/wt", "repo", "wt"}) {
		t.Fatalf("Open() args = %#v", backend.openArgs)
	}
}

func TestEnterWorktreeFallsBackAfterOpenFailure(t *testing.T) {
	openErr := errors.New("open failed")
	spawnErr := errors.New("spawn failed")
	backend := &fakeMultiplexer{name: "tmux", openErr: openErr}
	var spawnArgs []string
	spawn := func(dir, worktreeName string) error {
		spawnArgs = []string{dir, worktreeName}
		return spawnErr
	}

	err := enterWorktreeWithSpawner("/repo/wt", "repo", "wt", backend, spawn)
	if !errors.Is(err, spawnErr) {
		t.Fatalf("error = %v, want spawn error %v", err, spawnErr)
	}
	if !reflect.DeepEqual(spawnArgs, []string{"/repo/wt", "wt"}) {
		t.Fatalf("spawn args = %#v", spawnArgs)
	}
}

func TestEnterWorktreeSpawnsWithoutMultiplexer(t *testing.T) {
	called := false
	spawn := func(dir, worktreeName string) error {
		called = dir == "/repo/wt" && worktreeName == "wt"
		return nil
	}

	if err := enterWorktreeWithSpawner("/repo/wt", "repo", "wt", nil, spawn); err != nil {
		t.Fatalf("enterWorktreeWithSpawner() error = %v", err)
	}
	if !called {
		t.Fatal("spawn was not called with worktree arguments")
	}
}

func TestCloseWorkspaceDelegatesAndPropagatesError(t *testing.T) {
	wantErr := errors.New("close failed")
	backend := &fakeMultiplexer{name: "tmux", closeErr: wantErr}

	err := closeWorkspace(backend, "repo", "wt")
	if !errors.Is(err, wantErr) {
		t.Fatalf("closeWorkspace() error = %v, want %v", err, wantErr)
	}
	if !reflect.DeepEqual(backend.closeArgs, []string{"repo", "wt"}) {
		t.Fatalf("Close() args = %#v", backend.closeArgs)
	}
}

func TestCloseWorkspaceWithoutMultiplexerIsNoOp(t *testing.T) {
	if err := closeWorkspace(nil, "repo", "wt"); err != nil {
		t.Fatalf("closeWorkspace(nil) error = %v", err)
	}
}
```

- [ ] **Step 2: Run the helper tests to verify they fail**

Run:

```bash
go test ./internal/cli -run 'Test(EnterWorktree|CloseWorkspace)' -v
```

Expected: FAIL because the shared helpers are undefined.

- [ ] **Step 3: Implement the shared CLI helpers**

Create `internal/cli/multiplexer.go`:

```go
package cli

import (
	"github.com/joch/ygg/internal/multiplexer"
	"github.com/joch/ygg/internal/shell"
)

type shellSpawner func(dir, worktreeName string) error

func enterWorktree(dir, repoName, worktreeName string) error {
	return enterWorktreeWithSpawner(dir, repoName, worktreeName, multiplexer.Detect(), shell.Spawn)
}

func enterWorktreeWithSpawner(
	dir, repoName, worktreeName string,
	backend multiplexer.Backend,
	spawn shellSpawner,
) error {
	if backend != nil {
		info("Opening %s workspace...", backend.Name())
		if err := backend.Open(dir, repoName, worktreeName); err == nil {
			return nil
		} else {
			info("%s failed, falling back to subshell: %v", backend.Name(), err)
		}
	}

	info("Entering %s (exit to return)...", worktreeName)
	return spawn(dir, worktreeName)
}

func closeWorkspace(backend multiplexer.Backend, repoName, worktreeName string) error {
	if backend == nil {
		return nil
	}
	return backend.Close(repoName, worktreeName)
}
```

- [ ] **Step 4: Run the helper tests**

Run:

```bash
gofmt -w internal/cli/multiplexer.go internal/cli/multiplexer_test.go
go test ./internal/cli -run 'Test(EnterWorktree|CloseWorkspace)' -v
```

Expected: PASS.

- [ ] **Step 5: Replace direct Zellij logic in `new` and `switch`**

In `internal/cli/new.go`, remove the `shell` and `zellij` imports. Replace lines 95-106 with:

```go
	return enterWorktree(wt.Path, wm.RepoName(), wt.Name)
```

Do not move or change the `InYggShell()` block at lines 89-93.

In `internal/cli/switch.go`, remove the `shell` and `zellij` imports. Replace lines 56-67 with:

```go
	return enterWorktree(wt.Path, wm.RepoName(), wt.Name)
```

Do not move or change the `InYggShell()` block at lines 50-54.

- [ ] **Step 6: Replace direct Zellij cleanup in `remove` and `clean`**

In `internal/cli/remove.go`, replace the `zellij` import with `github.com/joch/ygg/internal/multiplexer`. Replace lines 114-119 with:

```go
	backend := multiplexer.Detect()
	if err := closeWorkspace(backend, wm.RepoName(), worktreeName); err != nil {
		info("Could not close %s workspace: %v", backend.Name(), err)
	}
```

This block stays after `wm.Remove` succeeds. `closeWorkspace` returns nil for a nil backend, so `backend.Name()` is evaluated only when an error proves a non-nil backend was called.

In `internal/cli/clean.go`, replace the `zellij` import with `github.com/joch/ygg/internal/multiplexer`. Replace lines 119-130 with:

```go
	backend := multiplexer.Detect()
	for _, wt := range toRemove {
		if err := wm.Remove(wt.Name); err != nil {
			errorMsg("Failed to remove %s: %v", wt.Name, err)
		} else {
			success("Removed %s", wt.Name)
			if err := closeWorkspace(backend, wm.RepoName(), wt.Name); err != nil {
				info("Could not close %s workspace: %v", backend.Name(), err)
			}
		}
	}
```

This detects one backend once, closes only after successful removal, and keeps later cleanup work running after close errors.

- [ ] **Step 7: Run CLI and full regression tests**

Run:

```bash
gofmt -w internal/cli/new.go internal/cli/switch.go internal/cli/remove.go internal/cli/clean.go
go test ./internal/cli ./internal/multiplexer ./internal/tmux ./internal/zellij -v
go test ./...
```

Expected: PASS. Existing Zellij tests continue to pass without modifications to `internal/zellij`.

- [ ] **Step 8: Commit the CLI integration**

```bash
git add internal/cli/multiplexer.go internal/cli/multiplexer_test.go internal/cli/new.go internal/cli/switch.go internal/cli/remove.go internal/cli/clean.go
git commit -m "feat: route worktrees through active multiplexer"
```

---

### Task 5: User-Facing Documentation and Final Verification

**Files:**
- Modify: `internal/cli/new.go:16-25`
- Modify: `internal/cli/switch.go:16-19`
- Modify: `README.md:39-44,54-60,116-123,146-150`
- Modify: `internal/skill/SKILL.md:12-18,29-35`

**Interfaces:**
- Consumes: the final CLI behavior from Task 4.
- Produces: accurate CLI help, README guidance, and bundled agent instructions; no new code API.

- [ ] **Step 1: Update command help**

In `internal/cli/new.go`, replace the third numbered behavior and final paragraph with:

```text
3. Open the worktree in the active tmux/Zellij multiplexer, or enter a
   subshell when no multiplexer is active

Exit the subshell to return to your original directory when ygg is not using
a multiplexer workspace.
```

In `internal/cli/switch.go`, replace the two-sentence long description body with:

```text
Inside tmux or Zellij, this focuses an existing named workspace or creates one.
Otherwise, it spawns a subshell in the worktree directory; exit that shell to
return to your original directory.
```

- [ ] **Step 2: Update README usage and integration documentation**

Change the `new` usage step to say:

```markdown
3. Open the worktree in the active tmux/Zellij multiplexer, or enter a subshell
```

Change the `switch` explanation to:

```markdown
Focuses or creates a named tmux/Zellij workspace when a supported multiplexer is active. Otherwise, enters a subshell in the specified worktree.
```

Replace `## Zellij Integration` with:

```markdown
## Terminal Multiplexer Integration

When running inside [tmux](https://github.com/tmux/tmux/wiki) or [Zellij](https://zellij.dev/), ygg automatically uses a named window/tab instead of spawning a subshell. No configuration is needed; ygg detects tmux via `$TMUX` and Zellij via `$ZELLIJ`.

- `ygg new my-feature` creates a workspace named `<repo>/my-feature` rooted at the new worktree
- `ygg switch my-feature` focuses the existing workspace, or creates one if it does not exist
- `ygg remove` and `ygg clean` close matching workspaces after successful worktree removal
- Tmux operations are limited to the current session

If both environments are present, tmux takes precedence. If opening a workspace fails, ygg reports the error and falls back to the normal subshell behavior. Cleanup failures are reported but do not undo worktree removal.
```

Replace the first paragraph under `## How it works` with:

```markdown
Ygg uses named tmux windows or Zellij tabs when invoked inside a supported multiplexer. Otherwise, it spawns subshells in worktree directories; when you're done, `exit` to return to where you started.
```

- [ ] **Step 3: Update the bundled ygg agent skill**

Change the `ygg new` command comment in `internal/skill/SKILL.md` to:

```text
ygg new <name>       # Create worktree + branch, then open a multiplexer workspace/subshell
```

Replace the Zellij-only key behavior with:

```markdown
- Tmux and Zellij are detected via `$TMUX`/`$ZELLIJ`: ygg opens or focuses a named `<repo>/<worktree>` workspace instead of nesting a shell
- Tmux takes precedence when both variables are set, and operates only in the current tmux session
- `ygg remove` and `ygg clean` close matching multiplexer workspaces after removing worktrees
```

- [ ] **Step 4: Run formatting, tests, race detection, vet, and build**

Run:

```bash
gofmt -w internal/cli/new.go internal/cli/switch.go
go test ./...
go test -race ./...
go vet ./...
go build -o /tmp/ygg-build-check ./cmd/ygg
git diff --check
git status --short internal/zellij
```

Expected: every Go command exits 0, `git diff --check` reports no whitespace errors, and `git status` prints nothing for `internal/zellij`.

- [ ] **Step 5: Perform tmux smoke checks**

Build a disposable binary:

```bash
go build -o /tmp/ygg-tmux-smoke ./cmd/ygg
```

From a live tmux client in a disposable Git repository, run:

```bash
/tmp/ygg-tmux-smoke new tmux-smoke
```

Verify the new current-session window is named `<repo>/tmux-smoke` and `pwd` is `<repo>/.worktrees/tmux-smoke`. From another window in the same session, run:

```bash
/tmp/ygg-tmux-smoke switch tmux-smoke
```

Verify it focuses the existing window rather than creating a duplicate. Merge the disposable branch or use the explicit destructive test choice `remove --force`, then verify the matching window closes:

```bash
/tmp/ygg-tmux-smoke remove --force tmux-smoke
```

Expected: creation, exact focus, working directory, and cleanup all match the acceptance criteria. Do not use `--force` outside the disposable smoke repository.

- [ ] **Step 6: Perform the Zellij regression smoke check when Zellij is available**

From a live Zellij session in a disposable Git repository, run:

```bash
/tmp/ygg-tmux-smoke new zellij-smoke
```

Verify the tab is named `<repo>/zellij-smoke`, its shell starts in `<repo>/.worktrees/zellij-smoke`, and `switch` focuses it. Remove it in the disposable repository:

```bash
/tmp/ygg-tmux-smoke remove --force zellij-smoke
```

Expected: existing Zellij creation, focus, working-directory, and cleanup behavior is unchanged. If Zellij is unavailable, record that the manual check was skipped; the unchanged source and automated Zellij tests remain required.

- [ ] **Step 7: Commit documentation and help**

```bash
git add README.md internal/skill/SKILL.md internal/cli/new.go internal/cli/switch.go
git commit -m "docs: document tmux multiplexer support"
```

- [ ] **Step 8: Confirm the final change set**

Run:

```bash
git status --short
git log --oneline -5
go test -race ./...
```

Expected: the worktree is clean, the five feature commits are visible, and the race-enabled suite passes.
