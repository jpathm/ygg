package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/joch/ygg/internal/linear"
)

// fakeIssues is a stub issueService returning canned results.
type fakeIssues struct {
	issue     *linear.Issue
	issueErr  error
	created   *linear.Issue
	createErr error

	createdTitle string
	createdTeam  string
}

func (f *fakeIssues) Issue(ctx context.Context, identifier string) (*linear.Issue, error) {
	return f.issue, f.issueErr
}

func (f *fakeIssues) CreateIssue(ctx context.Context, teamKey, title, desc string) (*linear.Issue, error) {
	f.createdTeam = teamKey
	f.createdTitle = title
	return f.created, f.createErr
}

func TestParseReference(t *testing.T) {
	tests := []struct {
		name    string
		wantRef string
		wantOK  bool
	}{
		{"snk-31-owl-have-cli", "SNK-31", true},
		{"SNK-31", "SNK-31", true},
		{"snk-31", "SNK-31", true},
		{"fix-2-things", "FIX-2", true},
		{"unified-tui", "", false},
		{"feat/auth", "", false},
		{"snk31", "", false},
		{"31-snk", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		gotRef, gotOK := parseReference(tt.name)
		if gotRef != tt.wantRef || gotOK != tt.wantOK {
			t.Errorf("parseReference(%q) = (%q, %v), want (%q, %v)",
				tt.name, gotRef, gotOK, tt.wantRef, tt.wantOK)
		}
	}
}

func TestIssueTitle(t *testing.T) {
	tests := []struct{ name, want string }{
		{"unified-tui", "Unified tui"},
		{"feat/auth", "Feat auth"},
		{"some_thing_here", "Some thing here"},
		{"x", "X"},
	}
	for _, tt := range tests {
		if got := issueTitle(tt.name); got != tt.want {
			t.Errorf("issueTitle(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestResolveNameReferenceShaped(t *testing.T) {
	found := &linear.Issue{
		Identifier: "SNK-31",
		Title:      "OWL - have cli also host pure html",
		BranchName: "snk-31-DIFFERENT-from-input",
	}

	tests := []struct {
		name     string
		svc      issueService
		wantNote string
	}{
		{
			name:     "found",
			svc:      &fakeIssues{issue: found},
			wantNote: "Linked to SNK-31 — OWL - have cli also host pure html",
		},
		{
			name:     "absent",
			svc:      &fakeIssues{issueErr: linear.ErrNotFound},
			wantNote: "SNK-31 does not exist in Linear — unlinked",
		},
		{
			name:     "no api key",
			svc:      nil,
			wantNote: "No LINEAR_API_KEY set — SNK-31 not verified",
		},
		{
			name:     "unreachable",
			svc:      &fakeIssues{issueErr: linear.ErrUnreachable},
			wantNote: "Could not reach Linear — SNK-31 not verified",
		},
		{
			name:     "unauthorized",
			svc:      &fakeIssues{issueErr: linear.ErrUnauthorized},
			wantNote: "Linear rejected the API key — SNK-31 not verified",
		},
		{
			name:     "other error",
			svc:      &fakeIssues{issueErr: errors.New("rate limited")},
			wantNote: "Could not verify SNK-31: rate limited",
		},
	}

	const input = "snk-31-owl-have-cli-also-host-pure-html"
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			branch, note := resolveName(context.Background(), tt.svc, "SKUNK", "GridKitLLC/otter-tools", input)
			// A reference-shaped name is always used exactly as typed.
			if branch != input {
				t.Errorf("branch = %q, want %q", branch, input)
			}
			if note != tt.wantNote {
				t.Errorf("note = %q, want %q", note, tt.wantNote)
			}
		})
	}
}

func TestResolveNamePlain(t *testing.T) {
	created := &linear.Issue{
		Identifier: "SNK-42",
		Title:      "Unified tui",
		BranchName: "snk-42-unified-tui",
		URL:        "https://linear.app/gridkit/issue/SNK-42/unified-tui",
	}

	tests := []struct {
		name       string
		svc        issueService
		team       string
		wantBranch string
		wantNote   string
	}{
		{
			name:       "created",
			svc:        &fakeIssues{created: created},
			team:       "SKUNK",
			wantBranch: "snk-42-unified-tui",
			wantNote:   "Created SNK-42 — https://linear.app/gridkit/issue/SNK-42/unified-tui",
		},
		{
			name:       "no api key",
			svc:        nil,
			team:       "SKUNK",
			wantBranch: "unified-tui",
			wantNote:   "No LINEAR_API_KEY set — worktree will be unlinked",
		},
		{
			name:       "no team",
			svc:        &fakeIssues{created: created},
			team:       "",
			wantBranch: "unified-tui",
			wantNote:   "No Linear team mapped for GridKitLLC/otter-tools — unlinked",
		},
		{
			name:       "unreachable",
			svc:        &fakeIssues{createErr: linear.ErrUnreachable},
			team:       "SKUNK",
			wantBranch: "unified-tui",
			wantNote:   "Could not reach Linear — unlinked",
		},
		{
			name:       "unauthorized",
			svc:        &fakeIssues{createErr: linear.ErrUnauthorized},
			team:       "SKUNK",
			wantBranch: "unified-tui",
			wantNote:   "Linear rejected the API key — unlinked",
		},
		{
			name:       "other error",
			svc:        &fakeIssues{createErr: errors.New("boom")},
			team:       "SKUNK",
			wantBranch: "unified-tui",
			wantNote:   "Could not create Linear issue: boom — unlinked",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			branch, note := resolveName(context.Background(), tt.svc, tt.team, "GridKitLLC/otter-tools", "unified-tui")
			if branch != tt.wantBranch {
				t.Errorf("branch = %q, want %q", branch, tt.wantBranch)
			}
			if note != tt.wantNote {
				t.Errorf("note = %q, want %q", note, tt.wantNote)
			}
		})
	}
}

func TestResolveNamePassesTitleAndTeam(t *testing.T) {
	f := &fakeIssues{created: &linear.Issue{Identifier: "SNK-42", BranchName: "snk-42-unified-tui"}}
	resolveName(context.Background(), f, "SKUNK", "GridKitLLC/otter-tools", "unified-tui")
	if f.createdTeam != "SKUNK" {
		t.Errorf("team = %q, want SKUNK", f.createdTeam)
	}
	if f.createdTitle != "Unified tui" {
		t.Errorf("title = %q, want %q", f.createdTitle, "Unified tui")
	}
}

func TestNewCmdLongDescribesLinear(t *testing.T) {
	for _, want := range []string{"Linear", "LINEAR_API_KEY"} {
		if !strings.Contains(newCmd.Long, want) {
			t.Errorf("newCmd.Long does not mention %q", want)
		}
	}
}
