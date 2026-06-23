package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// defaultBaseURL is the production GitHub REST API endpoint.
const defaultBaseURL = "https://api.github.com"

// PullRequest is the subset of fields used to create a PR.
type PullRequest struct {
	Title string `json:"title"`
	Head  string `json:"head"`
	Base  string `json:"base"`
	Body  string `json:"body"`
}

// Client creates pull requests via the GitHub REST API.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// NewClient returns a Client using token for Bearer authorization. A nil
// httpClient selects http.DefaultClient.
func NewClient(token string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		BaseURL: defaultBaseURL,
		Token:   token,
		HTTP:    httpClient,
	}
}

// CreatePullRequest opens a pull request and returns its HTML URL.
func (c *Client) CreatePullRequest(ctx context.Context, owner, repo string, pr PullRequest) (string, error) {
	if c.Token == "" {
		return "", fmt.Errorf("GitHub token is required to create a pull request")
	}

	u := fmt.Sprintf("%s/repos/%s/%s/pulls", c.BaseURL, owner, repo)
	body, err := json.Marshal(pr)
	if err != nil {
		return "", fmt.Errorf("encode pull request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create GitHub request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("create pull request: %w (ensure the token has 'pull-requests: write' and the workflow job has permission pull-requests: write)", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("GitHub API returned HTTP %d: %s (ensure the token has 'pull-requests: write' and the workflow job has permission pull-requests: write)", resp.StatusCode, string(b))
	}

	var result struct {
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode GitHub response: %w", err)
	}
	return result.HTMLURL, nil
}
