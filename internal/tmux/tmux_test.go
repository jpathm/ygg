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
