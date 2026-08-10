package multiplexer

import (
	"errors"
	"reflect"
	"testing"
)

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

func TestDetect(t *testing.T) {
	tests := []struct {
		name, herdr, tmux, zellij, want string
	}{
		{name: "Herdr only", herdr: "1", want: "Herdr"},
		{name: "tmux only", tmux: "set", want: "tmux"},
		{name: "Zellij only", zellij: "set", want: "zellij"},
		{name: "Herdr wins all markers", herdr: "1", tmux: "set", zellij: "set", want: "Herdr"},
		{name: "tmux wins nested Zellij", tmux: "set", zellij: "set", want: "tmux"},
		{name: "noncanonical Herdr ignored", herdr: "true", tmux: "set", want: "tmux"},
		{name: "none"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HERDR_ENV", tt.herdr)
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

func TestFunctionBackendDelegatesTarget(t *testing.T) {
	wantErr := errors.New("open failed")
	want := Target{
		Path: "/repo/.worktrees/auth", RepoName: "repo",
		WorktreeName: "auth", Branch: "feat/auth",
	}
	var openTarget, prepareTarget Target
	backend := functionBackend{
		name: "test", activeFn: func() bool { return true },
		openFn: func(target Target) error { openTarget = target; return wantErr },
		prepareCloseFn: func(target Target) (ClosePlan, error) {
			prepareTarget = target
			return NewClosePlan(true, nil), wantErr
		},
	}
	if err := backend.Open(want); !errors.Is(err, wantErr) {
		t.Fatalf("Open() error = %v, want %v", err, wantErr)
	}
	if !reflect.DeepEqual(openTarget, want) {
		t.Fatalf("Open() target = %#v, want %#v", openTarget, want)
	}
	plan, err := backend.PrepareClose(want)
	if !errors.Is(err, wantErr) {
		t.Fatalf("PrepareClose() error = %v, want %v", err, wantErr)
	}
	if !plan.Matched() {
		t.Fatal("PrepareClose() plan did not pass through")
	}
	if !reflect.DeepEqual(prepareTarget, want) {
		t.Fatalf("PrepareClose() target = %#v, want %#v", prepareTarget, want)
	}
}
