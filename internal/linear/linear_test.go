package linear

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
