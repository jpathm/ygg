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
