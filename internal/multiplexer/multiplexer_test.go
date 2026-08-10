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
		prepareCloseFn: func(Target) (ClosePlan, error) {
			return ClosePlan{}, nil
		},
	}
	if err := backend.Open(want); !errors.Is(err, wantErr) {
		t.Fatalf("Open() error = %v, want %v", err, wantErr)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Open() target = %#v, want %#v", got, want)
	}
}
