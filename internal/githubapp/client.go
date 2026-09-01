package githubapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.github.com"

// Client is a minimal GitHub REST API client authenticated with a single
// bearer token (either an App JWT, for the installation-token exchange, or
// an installation access token, for everything else).
type Client struct {
	httpClient *http.Client
	token      string
	baseURL    string
}

// ClientOption customizes a Client constructed by NewAppClient or
// NewInstallationClient.
type ClientOption func(*Client)

// WithBaseURL points the client at a different API base, e.g. a GitHub
// Enterprise Server instance's "/api/v3", or a test server.
func WithBaseURL(baseURL string) ClientOption {
	return func(c *Client) { c.baseURL = baseURL }
}

// NewAppClient returns a Client authenticated as the GitHub App itself
// (via a JWT), only valid for the installation-token exchange endpoint.
func NewAppClient(appJWT string, opts ...ClientOption) *Client {
	return newClient(appJWT, opts...)
}

// NewInstallationClient returns a Client authenticated as a GitHub App
// installation, valid for the repository-scoped endpoints it was granted.
func NewInstallationClient(installationToken string, opts ...ClientOption) *Client {
	return newClient(installationToken, opts...)
}

func newClient(token string, opts ...ClientOption) *Client {
	c := &Client{httpClient: &http.Client{Timeout: 10 * time.Second}, token: token, baseURL: defaultBaseURL}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// InstallationToken exchanges the App JWT held by c for a short-lived
// installation access token scoped to installationID.
func (c *Client) InstallationToken(ctx context.Context, installationID int64) (string, error) {
	var out struct {
		Token string `json:"token"`
	}
	path := fmt.Sprintf("/app/installations/%d/access_tokens", installationID)
	if err := c.do(ctx, http.MethodPost, path, bytes.NewReader([]byte("{}")), &out); err != nil {
		return "", err
	}
	return out.Token, nil
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("github api %s %s: status %d: %s", method, path, resp.StatusCode, data)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

// fetchAllPages GETs path and every subsequent page linked via the
// standard GitHub "Link: <url>; rel=\"next\"" response header.
func fetchAllPages[T any](ctx context.Context, c *Client, path string) ([]T, error) {
	var all []T
	next := c.baseURL + path

	for next != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, next, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Accept", "application/vnd.github+json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode >= 300 {
			return nil, fmt.Errorf("github api GET %s: status %d: %s", next, resp.StatusCode, data)
		}

		var page []T
		if err := json.Unmarshal(data, &page); err != nil {
			return nil, err
		}
		all = append(all, page...)
		next = nextPageURL(resp.Header.Get("Link"))
	}

	return all, nil
}

func nextPageURL(linkHeader string) string {
	for _, part := range strings.Split(linkHeader, ",") {
		segments := strings.Split(part, ";")
		if len(segments) < 2 {
			continue
		}
		if strings.TrimSpace(segments[1]) == `rel="next"` {
			return strings.Trim(strings.TrimSpace(segments[0]), "<>")
		}
	}
	return ""
}
