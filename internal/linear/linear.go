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
