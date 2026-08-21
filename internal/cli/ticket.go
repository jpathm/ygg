package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode"

	"github.com/joch/ygg/internal/config"
	"github.com/joch/ygg/internal/linear"
	"github.com/joch/ygg/internal/worktree"
)

// referencePattern matches a Linear branch name or bare identifier: a short
// alphabetic team key, a decimal issue number, and an optional title slug.
var referencePattern = regexp.MustCompile(`^([A-Za-z]+)-([0-9]+)(-.*)?$`)

// issueService is the slice of the Linear client that name resolution needs.
type issueService interface {
	Issue(ctx context.Context, identifier string) (*linear.Issue, error)
	CreateIssue(ctx context.Context, teamKey, title, desc string) (*linear.Issue, error)
}

// parseReference extracts the Linear issue identifier from a worktree name,
// so "snk-31-owl-have-cli" yields "SNK-31". The second result reports whether
// the name looked like a Linear reference at all.
func parseReference(name string) (string, bool) {
	m := referencePattern.FindStringSubmatch(name)
	if m == nil {
		return "", false
	}
	return strings.ToUpper(m[1]) + "-" + m[2], true
}

// issueTitle turns a worktree name into an issue title. It is deliberately
// naive: acronyms come out wrong, and renaming the issue in Linear afterwards
// does not affect the branch, which is captured at creation time.
func issueTitle(name string) string {
	s := strings.NewReplacer("-", " ", "_", " ", "/", " ").Replace(name)
	s = strings.TrimSpace(s)
	if s == "" {
		return name
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// resolveName maps a requested worktree name onto the branch name to create,
// plus a message to show the user.
//
// It returns no error by design. Linear must never prevent a worktree from
// being created, so every failure downgrades to a note and the requested name
// is used as-is. ygg picks the branch name only when ygg created the issue.
//
// svc may be nil, which means no API key is configured.
func resolveName(ctx context.Context, svc issueService, team, repo, name string) (branch, note string) {
	if ref, ok := parseReference(name); ok {
		// The user asserted a link. ygg is only confirming it, so it must not
		// claim the worktree is unlinked when it merely could not check.
		if svc == nil {
			return name, fmt.Sprintf("No LINEAR_API_KEY set — %s not verified", ref)
		}

		issue, err := svc.Issue(ctx, ref)
		switch {
		case errors.Is(err, linear.ErrNotFound):
			return name, fmt.Sprintf("%s does not exist in Linear — unlinked", ref)
		case errors.Is(err, linear.ErrUnauthorized):
			return name, fmt.Sprintf("Linear rejected the API key — %s not verified", ref)
		case errors.Is(err, linear.ErrUnreachable):
			return name, fmt.Sprintf("Could not reach Linear — %s not verified", ref)
		case err != nil:
			return name, fmt.Sprintf("Could not verify %s: %v", ref, err)
		}
		return name, fmt.Sprintf("Linked to %s — %s", issue.Identifier, issue.Title)
	}

	// ygg is responsible for producing the ticket, so any failure here really
	// does leave the worktree unlinked.
	switch {
	case svc == nil:
		return name, "No LINEAR_API_KEY set — worktree will be unlinked"
	case team == "":
		if repo == "" {
			return name, "No origin remote — unlinked"
		}
		return name, fmt.Sprintf("No Linear team mapped for %s — unlinked", repo)
	}

	desc := fmt.Sprintf("Created by ygg for %s.", repo)
	issue, err := svc.CreateIssue(ctx, team, issueTitle(name), desc)
	switch {
	case errors.Is(err, linear.ErrUnauthorized):
		return name, "Linear rejected the API key — unlinked"
	case errors.Is(err, linear.ErrUnreachable):
		return name, "Could not reach Linear — unlinked"
	case err != nil:
		return name, fmt.Sprintf("Could not create Linear issue: %v — unlinked", err)
	}
	// A successful creation should always carry a branch name, but guard
	// against an empty one anyway: Create("") resolves to the .worktrees
	// directory itself, which already exists, so ygg new would fail with a
	// confusing "worktree  already exists" error instead of ever creating
	// one — the one hole in the never-fail guarantee. Falling back to the
	// requested name keeps that guarantee intact while still reporting the
	// issue that was created.
	branch = issue.BranchName
	if branch == "" {
		branch = name
	}
	return branch, fmt.Sprintf("Created %s — %s", issue.Identifier, issue.URL)
}

// resolveTicket resolves the requested worktree name against Linear and prints
// whatever it learned. It returns the branch name to create, which is the
// requested name unless ygg created an issue for it.
func resolveTicket(ctx context.Context, wm *worktree.Manager, name string) string {
	cfg, err := config.Load()
	if err != nil {
		// A malformed config is reported rather than ignored: a silently
		// skipped typo would leave the user believing this feature is on.
		info("Warning: %v", err)
		cfg = config.Config{}
	}

	remote := wm.RemoteURL()
	repo := config.NormalizeRemote(remote)

	// A nil issueService means "no API key configured". Leaving svc as a nil
	// interface (rather than a typed nil pointer) is what makes the nil check
	// inside resolveName work.
	var svc issueService
	if key := os.Getenv("LINEAR_API_KEY"); key != "" {
		svc = linear.NewClient(key)
	}

	// cfg.TeamFor normalizes remote internally, so this re-derives the same
	// value as repo above — a redundant parse, not a redundant network call.
	// Avoiding it would mean giving TeamFor a variant that accepts an
	// already-normalized repo, which is a config API change out of scope
	// here, so it is left as is.
	branch, note := resolveName(ctx, svc, cfg.TeamFor(remote), repo, name)
	// Every path through resolveName returns a non-empty note, so this guard
	// is currently dead. Kept anyway as cheap defensive coding: it costs
	// nothing and prevents a blank info() line if a future resolveName path
	// ever returns "".
	if note != "" {
		info("%s", note)
	}
	return branch
}
