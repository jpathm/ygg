package cli

import (
	"errors"
	"reflect"
	"testing"

	"github.com/joch/ygg/internal/multiplexer"
	"github.com/joch/ygg/internal/worktree"
)

type fakeMultiplexer struct {
	name       string
	openErr    error
	closeErr   error
	openTarget multiplexer.Target
	closeArgs  []string
}

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

func (f *fakeMultiplexer) Name() string { return f.name }
func (f *fakeMultiplexer) Active() bool { return true }

func (f *fakeMultiplexer) Open(target multiplexer.Target) error {
	f.openTarget = target
	return f.openErr
}

func (f *fakeMultiplexer) Close(repoName, worktreeName string) error {
	f.closeArgs = []string{repoName, worktreeName}
	return f.closeErr
}

func TestEnterWorktreeUsesActiveMultiplexer(t *testing.T) {
	target := multiplexer.Target{Path: "/repo/wt", RepoName: "repo", WorktreeName: "wt"}
	backend := &fakeMultiplexer{name: "tmux"}
	spawned := false
	spawn := func(string, string) error {
		spawned = true
		return nil
	}

	if err := enterWorktreeWithSpawner(target, backend, spawn); err != nil {
		t.Fatalf("enterWorktreeWithSpawner() error = %v", err)
	}
	if spawned {
		t.Fatal("spawn called after successful backend open")
	}
	if !reflect.DeepEqual(backend.openTarget, target) {
		t.Fatalf("Open() target = %#v", backend.openTarget)
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

	err := enterWorktreeWithSpawner(multiplexer.Target{Path: "/repo/wt", RepoName: "repo", WorktreeName: "wt"}, backend, spawn)
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

	if err := enterWorktreeWithSpawner(multiplexer.Target{Path: "/repo/wt", RepoName: "repo", WorktreeName: "wt"}, nil, spawn); err != nil {
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
