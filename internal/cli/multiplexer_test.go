package cli

import (
	"errors"
	"reflect"
	"testing"

	"github.com/joch/ygg/internal/multiplexer"
	"github.com/joch/ygg/internal/worktree"
)

type fakeMultiplexer struct {
	name          string
	openErr       error
	prepareErr    error
	preparePlan   multiplexer.ClosePlan
	openTarget    multiplexer.Target
	prepareTarget multiplexer.Target
	order         *[]string
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

func (f *fakeMultiplexer) PrepareClose(target multiplexer.Target) (multiplexer.ClosePlan, error) {
	f.prepareTarget = target
	if f.order != nil {
		*f.order = append(*f.order, "prepare")
	}
	return f.preparePlan, f.prepareErr
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
