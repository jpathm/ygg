package herdr

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

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

func TestPrepareCloseEdgeCases(t *testing.T) {
	tests := []struct {
		name, output, wantID, wantError string
		commandErr                      error
		wantFound                       bool
	}{
		{name: "no match", output: `{"result":{"type":"workspace_list","workspaces":[]}}`},
		{name: "duplicate", output: `{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"w5","worktree":{"checkout_path":"/repo/wt"}},{"workspace_id":"w6","worktree":{"checkout_path":"/repo/wt"}}]}}`, wantError: "multiple Herdr workspaces"},
		{name: "command", output: "socket unavailable", commandErr: errors.New("exit 1"), wantError: "socket unavailable"},
		{name: "malformed JSON", output: "not-json", wantError: "failed to parse"},
		{name: "wrong type", output: `{"result":{"type":"workspace_info"}}`, wantError: "workspace_info"},
		{name: "missing workspaces", output: `{"result":{"type":"workspace_list"}}`, wantError: "workspaces"},
		{name: "null workspaces", output: `{"result":{"type":"workspace_list","workspaces":null}}`, wantError: "workspaces"},
		{name: "missing checkout path", output: `{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"w5","worktree":{}}]}}`, wantError: "checkout_path"},
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

func TestPrepareCloseAllowsUnknownWorkspaceListFields(t *testing.T) {
	fake := &fakeRunner{results: []commandResult{{output: `{
		"future_envelope_field":true,
		"result":{"type":"workspace_list","future_result_field":42,"workspaces":[
			{"workspace_id":"w5","future_workspace_field":"value","worktree":{"checkout_path":"/repo/wt","future_worktree_field":[]}}
		]}
	}`}}}
	useRunner(t, fake)

	id, found, err := PrepareClose("/repo/wt")
	if err != nil {
		t.Fatalf("PrepareClose() error = %v", err)
	}
	if id != "w5" || !found {
		t.Fatalf("PrepareClose() = (%q, %v), want (%q, true)", id, found, "w5")
	}
}

func TestCloseWorkspaceAcceptsOKResponse(t *testing.T) {
	fake := &fakeRunner{results: []commandResult{{output: `{"id":"cli:workspace:close","result":{"type":"ok"}}`}}}
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
