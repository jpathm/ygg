# Linear Ticket Linking for `ygg new` — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `ygg new <name>` resolve the requested name against Linear before creating a branch, so a worktree normally carries a Linear ticket from the moment it exists.

**Architecture:** A new `internal/linear` package speaks Linear's GraphQL API over stdlib `net/http` and exposes exactly two operations plus three error sentinels. A new `internal/config` package loads `~/.config/ygg/config.json` and maps a normalized `owner/repo` remote onto a Linear team key. `internal/cli` gains a `resolveName` function that returns `(branch, note string)` and no error — warn-and-proceed is enforced by that signature. `runNew` calls it once, before `wm.Create`.

**Tech Stack:** Go 1.24, stdlib only (`net/http`, `encoding/json`, `regexp`, `os`), plus ygg's existing `cobra` and `fatih/color`. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-08-20-linear-ticket-gate-design.md`

**Linear ticket:** SNK-33

## Global Constraints

- **No new Go dependencies.** `go.mod` must be unchanged at the end of this plan. If a task seems to need a library, it does not.
- **Only `ygg new` changes behavior.** `ygg list`, `ygg switch`, `ygg remove`, and `ygg clean` must be untouched.
- **`ygg new` must never fail because of Linear.** Every Linear error path prints a warning via `info(...)` and proceeds to create the worktree.
- **Reference pattern is exactly** `^([A-Za-z]+)-([0-9]+)(-.*)?$`.
- **Config path** is `config.json` inside `os.UserConfigDir()` — that is `~/.config/ygg/config.json` on Linux and honors `XDG_CONFIG_HOME`.
- **The API key comes from `LINEAR_API_KEY` and nowhere else.** It must never be read from the config file.
- **Endpoint** is `https://api.linear.app/graphql`, with the key sent raw in the `Authorization` header — no `Bearer` prefix.
- **Output helpers** already exist in `internal/cli/root.go`: `success`, `errorMsg`, `info`. There is no `warn`; warnings use `info`, matching the existing `info("Warning: ...")` calls in `new.go`.
- **Test style:** table-driven, hand-rolled fakes, `httptest` for HTTP. No test libraries.
- Run `go test ./...` and `go vet ./...` before every commit.

---

### Task 1: Linear client — issue lookup

**Files:**
- Create: `internal/linear/linear.go`
- Test: `internal/linear/linear_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `linear.Issue{Identifier, Title, BranchName, URL string}`; `linear.NewClient(apiKey string) *Client`; `(*Client).Issue(ctx context.Context, identifier string) (*Issue, error)`; sentinels `linear.ErrNotFound`, `linear.ErrUnauthorized`, `linear.ErrUnreachable`.

- [ ] **Step 1: Write the failing test**

Create `internal/linear/linear_test.go`:

```go
package linear

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestClient points a client at a stub server.
func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := NewClient("test-key")
	c.endpoint = srv.URL
	return c
}

func TestIssueReturnsIssue(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "test-key" {
			t.Errorf("Authorization = %q, want %q (no Bearer prefix)", got, "test-key")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"issue":{
			"identifier":"SNK-31",
			"title":"OWL - have cli also host pure html",
			"branchName":"snk-31-owl-have-cli-also-host-pure-html",
			"url":"https://linear.app/gridkit/issue/SNK-31/x"}}}`))
	})

	issue, err := c.Issue(context.Background(), "SNK-31")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if issue.Identifier != "SNK-31" {
		t.Errorf("Identifier = %q, want SNK-31", issue.Identifier)
	}
	if issue.BranchName != "snk-31-owl-have-cli-also-host-pure-html" {
		t.Errorf("BranchName = %q", issue.BranchName)
	}
}

func TestIssueErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr error
	}{
		{
			name:    "null issue is not found",
			status:  200,
			body:    `{"data":{"issue":null}}`,
			wantErr: ErrNotFound,
		},
		{
			name:    "entity not found error is not found",
			status:  200,
			body:    `{"errors":[{"message":"Entity not found: Issue"}]}`,
			wantErr: ErrNotFound,
		},
		{
			name:    "401 is unauthorized",
			status:  401,
			body:    `{}`,
			wantErr: ErrUnauthorized,
		},
		{
			name:    "403 is unauthorized",
			status:  403,
			body:    `{}`,
			wantErr: ErrUnauthorized,
		},
		{
			name:    "500 is unreachable",
			status:  500,
			body:    `oops`,
			wantErr: ErrUnreachable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			})
			_, err := c.Issue(context.Background(), "SNK-31")
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Issue() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestIssueGraphQLErrorIsReported(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"errors":[{"message":"rate limited"}]}`))
	})
	_, err := c.Issue(context.Background(), "SNK-31")
	if err == nil {
		t.Fatal("Issue() error = nil, want an error")
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrUnauthorized) {
		t.Errorf("Issue() error = %v, want a plain error", err)
	}
}

func TestIssueTransportFailureIsUnreachable(t *testing.T) {
	// A server that is created and immediately closed gives a deterministic
	// connection refusal, which reaches the same path as a timeout.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	c := NewClient("test-key")
	c.endpoint = url

	_, err := c.Issue(context.Background(), "SNK-31")
	if !errors.Is(err, ErrUnreachable) {
		t.Errorf("Issue() error = %v, want ErrUnreachable", err)
	}
}

func TestIssueUndecodableBody(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	})
	if _, err := c.Issue(context.Background(), "SNK-31"); err == nil {
		t.Fatal("Issue() error = nil, want a decode error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/linear/ -v`
Expected: FAIL — the package does not compile, `undefined: NewClient`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/linear/linear.go`:

```go
// Package linear is a minimal client for the Linear GraphQL API. It exists so
// that ygg can attach worktrees to Linear issues; it deliberately covers only
// the two operations ygg needs.
package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultEndpoint = "https://api.linear.app/graphql"

// Sentinel errors. Callers distinguish these three cases because each produces
// a different warning in `ygg new`; everything else is reported verbatim.
var (
	// ErrNotFound means Linear has no such issue.
	ErrNotFound = errors.New("linear: issue not found")
	// ErrUnauthorized means Linear rejected the API key.
	ErrUnauthorized = errors.New("linear: unauthorized")
	// ErrUnreachable means the request never produced a usable response.
	ErrUnreachable = errors.New("linear: unreachable")
)

// Issue is the subset of a Linear issue that ygg uses.
type Issue struct {
	Identifier string `json:"identifier"`
	Title      string `json:"title"`
	BranchName string `json:"branchName"`
	URL        string `json:"url"`
}

// Client talks to the Linear GraphQL API.
type Client struct {
	apiKey   string
	endpoint string
	http     *http.Client
}

// NewClient returns a client authenticating with the given personal API key.
func NewClient(apiKey string) *Client {
	return &Client{
		apiKey:   apiKey,
		endpoint: defaultEndpoint,
		// An unreachable Linear must degrade quickly rather than stall
		// worktree creation.
		http: &http.Client{Timeout: 10 * time.Second},
	}
}

type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

// do posts a GraphQL document and decodes the "data" object into out.
func (c *Client) do(ctx context.Context, query string, vars map[string]any, out any) error {
	body, err := json.Marshal(graphQLRequest{Query: query, Variables: vars})
	if err != nil {
		return fmt.Errorf("linear: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("linear: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Linear personal API keys are sent raw, without a "Bearer" prefix.
	req.Header.Set("Authorization", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnreachable, err)
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return ErrUnauthorized
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return fmt.Errorf("%w: status %d", ErrUnreachable, resp.StatusCode)
	}

	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("linear: decode response: %w", err)
	}

	if len(envelope.Errors) > 0 {
		msg := envelope.Errors[0].Message
		// Linear reports a missing entity as a GraphQL error rather than a
		// null field, so it has to be recognised here.
		if strings.Contains(strings.ToLower(msg), "not found") {
			return ErrNotFound
		}
		return fmt.Errorf("linear: %s", msg)
	}

	if len(envelope.Data) == 0 {
		return fmt.Errorf("linear: response contained no data")
	}
	return json.Unmarshal(envelope.Data, out)
}

const issueQuery = `query($id: String!) {
  issue(id: $id) { identifier title branchName url }
}`

// Issue looks up an issue by its human identifier, such as "SNK-31".
func (c *Client) Issue(ctx context.Context, identifier string) (*Issue, error) {
	var data struct {
		Issue *Issue `json:"issue"`
	}
	if err := c.do(ctx, issueQuery, map[string]any{"id": identifier}, &data); err != nil {
		return nil, err
	}
	if data.Issue == nil {
		return nil, ErrNotFound
	}
	return data.Issue, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/linear/ -v && go vet ./internal/linear/`
Expected: PASS, no vet findings.

- [ ] **Step 5: Commit**

```bash
git add internal/linear/linear.go internal/linear/linear_test.go
git commit -m "feat(linear): add GraphQL client with issue lookup"
```

---

### Task 2: Linear client — issue creation

**Files:**
- Modify: `internal/linear/linear.go` (append)
- Modify: `internal/linear/linear_test.go` (append)

**Interfaces:**
- Consumes: `Client.do`, `Issue`, sentinels from Task 1.
- Produces: `(*Client).CreateIssue(ctx context.Context, teamKey, title, desc string) (*Issue, error)`.

Creation is two round trips: a team key such as `SKUNK` must be resolved to a team UUID before `issueCreate` will accept it.

- [ ] **Step 1: Write the failing test**

Append to `internal/linear/linear_test.go`:

```go
// respondInOrder returns each body in sequence, one per request.
func respondInOrder(t *testing.T, bodies ...string) http.HandlerFunc {
	t.Helper()
	var n int
	return func(w http.ResponseWriter, r *http.Request) {
		if n >= len(bodies) {
			t.Errorf("unexpected request %d", n+1)
			w.WriteHeader(500)
			return
		}
		_, _ = w.Write([]byte(bodies[n]))
		n++
	}
}

func TestCreateIssue(t *testing.T) {
	c := newTestClient(t, respondInOrder(t,
		`{"data":{"teams":{"nodes":[{"id":"team-uuid"}]}}}`,
		`{"data":{"issueCreate":{"success":true,"issue":{
			"identifier":"SNK-42",
			"title":"Unified tui",
			"branchName":"snk-42-unified-tui",
			"url":"https://linear.app/gridkit/issue/SNK-42/unified-tui"}}}}`,
	))

	issue, err := c.CreateIssue(context.Background(), "SKUNK", "Unified tui", "Created by ygg.")
	if err != nil {
		t.Fatalf("CreateIssue() error = %v", err)
	}
	if issue.Identifier != "SNK-42" {
		t.Errorf("Identifier = %q, want SNK-42", issue.Identifier)
	}
	if issue.BranchName != "snk-42-unified-tui" {
		t.Errorf("BranchName = %q, want snk-42-unified-tui", issue.BranchName)
	}
}

func TestCreateIssueUnknownTeam(t *testing.T) {
	c := newTestClient(t, respondInOrder(t,
		`{"data":{"teams":{"nodes":[]}}}`,
	))
	_, err := c.CreateIssue(context.Background(), "NOPE", "x", "y")
	if err == nil {
		t.Fatal("CreateIssue() error = nil, want an error")
	}
	if !strings.Contains(err.Error(), "NOPE") {
		t.Errorf("error = %v, want it to name the team key", err)
	}
}

func TestCreateIssueUnauthorizedPropagates(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	})
	_, err := c.CreateIssue(context.Background(), "SKUNK", "x", "y")
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("error = %v, want ErrUnauthorized", err)
	}
}

func TestCreateIssueUnsuccessful(t *testing.T) {
	c := newTestClient(t, respondInOrder(t,
		`{"data":{"teams":{"nodes":[{"id":"team-uuid"}]}}}`,
		`{"data":{"issueCreate":{"success":false,"issue":null}}}`,
	))
	if _, err := c.CreateIssue(context.Background(), "SKUNK", "x", "y"); err == nil {
		t.Fatal("CreateIssue() error = nil, want an error")
	}
}
```

Add `"strings"` to that file's imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/linear/ -run CreateIssue -v`
Expected: FAIL — `c.CreateIssue undefined`.

- [ ] **Step 3: Write minimal implementation**

Append to `internal/linear/linear.go`:

```go
const teamQuery = `query($key: String!) {
  teams(filter: {key: {eq: $key}}, first: 1) { nodes { id } }
}`

const createIssueMutation = `mutation($input: IssueCreateInput!) {
  issueCreate(input: $input) {
    success
    issue { identifier title branchName url }
  }
}`

// teamID resolves a team key such as "SKUNK" to its UUID, which is what
// issueCreate requires.
func (c *Client) teamID(ctx context.Context, key string) (string, error) {
	var data struct {
		Teams struct {
			Nodes []struct {
				ID string `json:"id"`
			} `json:"nodes"`
		} `json:"teams"`
	}
	if err := c.do(ctx, teamQuery, map[string]any{"key": key}, &data); err != nil {
		return "", err
	}
	if len(data.Teams.Nodes) == 0 {
		return "", fmt.Errorf("linear: no team with key %q", key)
	}
	return data.Teams.Nodes[0].ID, nil
}

// CreateIssue files a new issue on the given team and returns it. State and
// assignee are left to Linear's defaults.
func (c *Client) CreateIssue(ctx context.Context, teamKey, title, desc string) (*Issue, error) {
	teamID, err := c.teamID(ctx, teamKey)
	if err != nil {
		return nil, err
	}

	var data struct {
		IssueCreate struct {
			Success bool   `json:"success"`
			Issue   *Issue `json:"issue"`
		} `json:"issueCreate"`
	}
	input := map[string]any{
		"teamId":      teamID,
		"title":       title,
		"description": desc,
	}
	if err := c.do(ctx, createIssueMutation, map[string]any{"input": input}, &data); err != nil {
		return nil, err
	}
	if !data.IssueCreate.Success || data.IssueCreate.Issue == nil {
		return nil, fmt.Errorf("linear: issue creation did not succeed")
	}
	return data.IssueCreate.Issue, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/linear/ -v && go vet ./internal/linear/`
Expected: PASS, no vet findings.

- [ ] **Step 5: Commit**

```bash
git add internal/linear/
git commit -m "feat(linear): add issue creation with team key resolution"
```

---

### Task 3: Config loading and team resolution

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `config.Config` with field `Linear` of type `config.Linear{DefaultTeam string; Teams map[string]string}`; `config.Load() (Config, error)`; `config.Path() (string, error)`; `config.NormalizeRemote(remote string) string`; `(Config).TeamFor(remote string) string`.

`Load` returns a zero `Config` and a nil error when the file is absent — an unconfigured machine is not an error. It returns an error only when the file exists and does not parse.

- [ ] **Step 1: Write the failing test**

Create `internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeConfig points os.UserConfigDir at a temp dir and writes body to
// ygg/config.json inside it. Passing an empty body writes no file at all.
func writeConfig(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if body == "" {
		return
	}
	if err := os.MkdirAll(filepath.Join(dir, "ygg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ygg", "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	writeConfig(t, "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if cfg.Linear.DefaultTeam != "" || len(cfg.Linear.Teams) != 0 {
		t.Errorf("Load() = %+v, want zero Config", cfg)
	}
}

func TestLoadParsesConfig(t *testing.T) {
	writeConfig(t, `{"linear":{"defaultTeam":"SKUNK","teams":{"GridKitLLC/ygg":"SKUNK"}}}`)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Linear.DefaultTeam != "SKUNK" {
		t.Errorf("DefaultTeam = %q, want SKUNK", cfg.Linear.DefaultTeam)
	}
	if cfg.Linear.Teams["GridKitLLC/ygg"] != "SKUNK" {
		t.Errorf("Teams = %v", cfg.Linear.Teams)
	}
}

func TestLoadMalformedFileIsAnError(t *testing.T) {
	writeConfig(t, `{"linear":`)
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want a parse error")
	}
}

func TestNormalizeRemote(t *testing.T) {
	tests := []struct {
		remote string
		want   string
	}{
		{"git@github.com:GridKitLLC/ygg.git", "GridKitLLC/ygg"},
		{"git@github.com:GridKitLLC/ygg", "GridKitLLC/ygg"},
		{"https://github.com/GridKitLLC/ygg.git", "GridKitLLC/ygg"},
		{"https://github.com/GridKitLLC/ygg", "GridKitLLC/ygg"},
		{"ssh://git@github.com/GridKitLLC/ygg.git", "GridKitLLC/ygg"},
		{"https://user@github.com/GridKitLLC/ygg", "GridKitLLC/ygg"},
		{"  https://github.com/GridKitLLC/ygg  ", "GridKitLLC/ygg"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := NormalizeRemote(tt.remote); got != tt.want {
			t.Errorf("NormalizeRemote(%q) = %q, want %q", tt.remote, got, tt.want)
		}
	}
}

func TestTeamFor(t *testing.T) {
	cfg := Config{Linear: Linear{
		DefaultTeam: "HELIUM",
		Teams:       map[string]string{"GridKitLLC/ygg": "SKUNK"},
	}}

	tests := []struct {
		name   string
		cfg    Config
		remote string
		want   string
	}{
		{"exact match wins", cfg, "git@github.com:GridKitLLC/ygg.git", "SKUNK"},
		{"unmapped falls back to default", cfg, "git@github.com:other/thing.git", "HELIUM"},
		{"no default and no match yields empty", Config{}, "git@github.com:other/thing.git", ""},
		{"empty remote falls back to default", cfg, "", "HELIUM"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.TeamFor(tt.remote); got != tt.want {
				t.Errorf("TeamFor(%q) = %q, want %q", tt.remote, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -v`
Expected: FAIL — the package does not compile, `undefined: Load`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/config/config.go`:

```go
// Package config loads ygg's optional user configuration. ygg works without a
// config file; the file only enables features that need per-repository
// settings, such as Linear team routing.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Linear holds Linear-specific settings. It never holds credentials — the API
// key comes from the LINEAR_API_KEY environment variable so that a secret is
// not written to a file that may be synchronised between machines.
type Linear struct {
	// DefaultTeam is the team key used when no per-repository entry matches.
	DefaultTeam string `json:"defaultTeam"`
	// Teams maps a normalized "owner/repo" remote onto a Linear team key.
	Teams map[string]string `json:"teams"`
}

// Config is ygg's user configuration.
type Config struct {
	Linear Linear `json:"linear"`
}

// Path returns the location of ygg's config file. It honors XDG_CONFIG_HOME.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("could not locate user config dir: %w", err)
	}
	return filepath.Join(dir, "ygg", "config.json"), nil
}

// Load reads the config file. A missing file yields a zero Config and no
// error, so installing a config-dependent feature never breaks ygg on a
// machine that has not been configured. A malformed file is an error, because
// silently ignoring a typo would leave the user believing a feature is active
// when it is not.
func Load() (Config, error) {
	var cfg Config

	path, err := Path()
	if err != nil {
		return cfg, err
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("could not read %s: %w", path, err)
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("could not parse %s: %w", path, err)
	}
	return cfg, nil
}

// NormalizeRemote reduces a git remote URL to "owner/repo", so that SSH and
// HTTPS remotes for the same repository produce the same key.
func NormalizeRemote(remote string) string {
	s := strings.TrimSpace(remote)
	s = strings.TrimSuffix(s, ".git")

	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
		// Drop everything up to the first slash: userinfo and host.
		if j := strings.Index(s, "/"); j >= 0 {
			s = s[j+1:]
		}
	} else if i := strings.Index(s, ":"); i >= 0 && strings.Contains(s[:i], "@") {
		// scp-style: git@github.com:owner/repo
		s = s[i+1:]
	}

	return strings.Trim(s, "/")
}

// TeamFor returns the Linear team key for a remote URL, preferring an exact
// per-repository entry and falling back to DefaultTeam. It returns "" when
// neither is configured.
func (c Config) TeamFor(remote string) string {
	if team, ok := c.Linear.Teams[NormalizeRemote(remote)]; ok && team != "" {
		return team
	}
	return c.Linear.DefaultTeam
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ -v && go vet ./internal/config/`
Expected: PASS, no vet findings.

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat(config): add user config with Linear team routing"
```

---

### Task 4: Expose the origin remote URL

**Files:**
- Modify: `internal/worktree/worktree.go` (append a method)
- Test: `internal/worktree/worktree_test.go` (append)

**Interfaces:**
- Consumes: existing `worktree.Manager`.
- Produces: `(*Manager).RemoteURL() string` — the `origin` URL, or `""` when there is no origin.

It returns a bare string rather than `(string, error)` because a repository with no origin is an ordinary case, not a failure, and every caller would discard the error. This matches `HasUncommittedChanges`, which already swallows its error.

- [ ] **Step 1: Write the failing test**

Append to `internal/worktree/worktree_test.go`:

```go
func TestRemoteURL(t *testing.T) {
	dir := t.TempDir()
	gitOut(t, dir, "init", "-q")
	gitOut(t, dir, "remote", "add", "origin", "git@github.com:GridKitLLC/ygg.git")

	m, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	if got := m.RemoteURL(); got != "git@github.com:GridKitLLC/ygg.git" {
		t.Errorf("RemoteURL() = %q", got)
	}
}

func TestRemoteURLWithoutOrigin(t *testing.T) {
	dir := t.TempDir()
	gitOut(t, dir, "init", "-q")

	m, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	if got := m.RemoteURL(); got != "" {
		t.Errorf("RemoteURL() = %q, want empty", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/worktree/ -run RemoteURL -v`
Expected: FAIL — `m.RemoteURL undefined`.

- [ ] **Step 3: Write minimal implementation**

Append to `internal/worktree/worktree.go`:

```go
// RemoteURL returns the URL of the origin remote, or "" when the repository
// has no origin. A missing remote is an ordinary case rather than a failure.
func (m *Manager) RemoteURL() string {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = m.repoPath
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/worktree/ -v && go vet ./internal/worktree/`
Expected: PASS, no vet findings.

- [ ] **Step 5: Commit**

```bash
git add internal/worktree/
git commit -m "feat(worktree): expose the origin remote URL"
```

---

### Task 5: Name resolution policy

**Files:**
- Create: `internal/cli/ticket.go`
- Test: `internal/cli/ticket_test.go`

**Interfaces:**
- Consumes: `linear.Issue`, `linear.ErrNotFound`, `linear.ErrUnauthorized`, `linear.ErrUnreachable` (Task 1).
- Produces: `issueService` interface; `parseReference(name string) (string, bool)`; `issueTitle(name string) string`; `resolveName(ctx context.Context, svc issueService, team, repo, name string) (branch, note string)`.

This task is the feature's policy, expressed as a pure function. `resolveName` returns no error on purpose: warn-and-proceed is the entire policy, and encoding it in the signature makes it structurally impossible for a later change to introduce a path where `ygg new` blocks on Linear.

The test table below *is* the specification. Keep it in sync with the message tables in the design doc.

- [ ] **Step 1: Write the failing test**

Create `internal/cli/ticket_test.go`:

```go
package cli

import (
	"context"
	"errors"
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
		BranchName: "snk-31-owl-have-cli-also-host-pure-html",
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run 'ParseReference|IssueTitle|ResolveName' -v`
Expected: FAIL — `undefined: parseReference`, `undefined: resolveName`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/cli/ticket.go`:

```go
package cli

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/joch/ygg/internal/linear"
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
	return issue.BranchName, fmt.Sprintf("Created %s — %s", issue.Identifier, issue.URL)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/ -v && go vet ./internal/cli/`
Expected: PASS, no vet findings.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/ticket.go internal/cli/ticket_test.go
git commit -m "feat(cli): add Linear name resolution policy"
```

---

### Task 6: Wire resolution into `ygg new`, and document it

**Files:**
- Modify: `internal/cli/ticket.go` (append `resolveTicket`)
- Modify: `internal/cli/new.go` (call it; rewrite `newCmd.Long`)
- Modify: `README.md`
- Test: `internal/cli/ticket_test.go` (append), `internal/cli/documentation_test.go` (extend)

**Interfaces:**
- Consumes: `resolveName` (Task 5), `config.Load`, `config.NormalizeRemote`, `(Config).TeamFor` (Task 3), `(*worktree.Manager).RemoteURL` (Task 4), `linear.NewClient` (Task 1).
- Produces: nothing consumed by later tasks. This is the last task.

Config loading lives here rather than inside `resolveName` because a parse error has to be reported before resolution begins, and `resolveName`'s return values have no room for it. On a parse error, `runNew` prints the warning and continues with the zero `Config`, which is indistinguishable from an unmapped repository.

- [ ] **Step 1: Write the failing test**

Append to `internal/cli/ticket_test.go`:

```go
func TestNewCmdLongDescribesLinear(t *testing.T) {
	for _, want := range []string{"Linear", "LINEAR_API_KEY"} {
		if !strings.Contains(newCmd.Long, want) {
			t.Errorf("newCmd.Long does not mention %q", want)
		}
	}
}
```

Add `"strings"` to that file's imports.

Also append to `internal/cli/documentation_test.go`, so the existing guarantee and the new one are checked together:

```go
func TestNewHelpMentionsUnlinkedWarning(t *testing.T) {
	if !strings.Contains(newCmd.Long, "unlinked") {
		t.Error("new help does not explain the unlinked case")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run 'Documentation|NewCmdLong|NewHelp' -v`
Expected: FAIL — `newCmd.Long` does not mention Linear.

- [ ] **Step 3: Write the implementation**

Append to `internal/cli/ticket.go`:

```go
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

	// A nil issueService means "no API key configured". Leaving svc as a nil
	// interface (rather than a typed nil pointer) is what makes the nil check
	// inside resolveName work.
	var svc issueService
	if key := os.Getenv("LINEAR_API_KEY"); key != "" {
		svc = linear.NewClient(key)
	}

	branch, note := resolveName(ctx, svc, cfg.TeamFor(remote), config.NormalizeRemote(remote), name)
	if note != "" {
		info("%s", note)
	}
	return branch
}
```

Add `"os"`, `"github.com/joch/ygg/internal/config"`, and `"github.com/joch/ygg/internal/worktree"` to that file's imports.

In `internal/cli/new.go`, insert the call after the default branch is detected and before the `info("Creating worktree: ...")` line, so the notes read chronologically:

```go
	// Resolve the requested name against Linear before creating the branch.
	// This can rename the worktree, so it must run before wm.Create.
	name = resolveTicket(cmd.Context(), wm, name)

	info("Creating worktree: %s (default branch %s)", name, defaultBranch)
```

Replace `newCmd.Long` in `internal/cli/new.go` with:

```go
	Long: `Create a new git worktree with the specified name.

This will:
1. Fetch the latest changes from origin
2. Resolve <name> against Linear. A name that looks like a Linear branch or
   identifier (for example snk-31-add-widget) is verified and used as typed.
   Any other name creates a Linear issue in the team mapped to this
   repository, and the worktree takes that issue's branch name.
3. Create a new worktree with a branch named <name>, based on the latest
   origin/<default-branch> (falling back to the local default branch when
   there is no remote)
4. Open the worktree in the active Herdr/tmux/Zellij workspace manager, or
   enter a subshell when no workspace backend is active

Linear is optional and never blocks. Without LINEAR_API_KEY, without a team
mapped in ~/.config/ygg/config.json, or when Linear cannot be reached, ygg
prints a warning and creates an unlinked worktree.

Exit the subshell to return to your original directory when ygg is not using
a workspace.`,
```

Note that this keeps the word "Herdr", which `documentation_test.go` requires.

Add to `README.md`, after the existing command documentation:

````markdown
## Linear integration

`ygg new` links worktrees to Linear issues. It is optional, and it never
prevents a worktree from being created.

A name that looks like a Linear branch or identifier is verified and used
exactly as typed:

```sh
ygg new snk-31-owl-have-cli-also-host-pure-html
# ℹ Linked to SNK-31 — OWL - have cli also host pure html
```

Any other name creates an issue and adopts its branch name:

```sh
ygg new unified-tui
# ℹ Created SNK-42 — https://linear.app/gridkit/issue/SNK-42/unified-tui
# ✓ Created worktree at .worktrees/snk-42-unified-tui
```

### Setup

Export a Linear personal API key, created under Settings → API in Linear:

```sh
export LINEAR_API_KEY=lin_api_...
```

Then map repositories onto Linear teams in `~/.config/ygg/config.json`:

```json
{
  "linear": {
    "defaultTeam": "SKUNK",
    "teams": {
      "GridKitLLC/otter-tools": "SKUNK",
      "GridKitLLC/ygg": "SKUNK"
    }
  }
}
```

Keys are `owner/repo`, matched against the `origin` remote; SSH and HTTPS
remotes both work. `defaultTeam` is used when no entry matches. The API key is
never read from this file.

### When Linear is unavailable

Every one of these prints a warning and still creates the worktree:

| Situation | Result |
| --- | --- |
| `LINEAR_API_KEY` unset | Unlinked worktree |
| Repository unmapped and no `defaultTeam` | Unlinked worktree |
| `config.json` malformed | Warning, then treated as unconfigured |
| Linear unreachable | Unlinked worktree |
| API key rejected | Unlinked worktree |
| Named issue does not exist | Worktree created under the name as typed |
````

- [ ] **Step 4: Run the full suite**

Run: `go test ./... && go vet ./... && git diff --exit-code go.mod`
Expected: all tests PASS, no vet findings, and `go.mod` unchanged — this feature adds no dependencies.

- [ ] **Step 5: Manual smoke test**

```bash
go build -o /tmp/ygg-snk33 ./cmd/ygg

# No key: must warn and still work.
env -u LINEAR_API_KEY /tmp/ygg-snk33 new --help

# Confirm the help text describes the Linear step.
/tmp/ygg-snk33 new --help | grep -i linear
```

Expected: help output mentions Linear, `LINEAR_API_KEY`, and Herdr.

Then, in a scratch clone with a real key exported, run `ygg new some-throwaway-name` once and confirm a Linear issue is created and the worktree takes its branch name. Delete both afterwards.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/ README.md
git commit -m "feat(cli): resolve ygg new names against Linear"
```

---

## Verification

Before opening a pull request:

- [ ] `go test -race ./...` passes.
- [ ] `go vet ./...` is clean.
- [ ] `git diff origin/main -- go.mod go.sum` is empty.
- [ ] `git diff origin/main --stat` touches only: `internal/linear/`, `internal/config/`, `internal/cli/ticket.go`, `internal/cli/ticket_test.go`, `internal/cli/new.go`, `internal/cli/documentation_test.go`, `internal/worktree/worktree.go`, `internal/worktree/worktree_test.go`, `README.md`, and the two `docs/superpowers/` files.
- [ ] `ygg list`, `ygg switch`, `ygg remove`, and `ygg clean` have no diff.
