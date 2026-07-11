// Package radiofrance is a client for Radio France's unofficial mobile-app
// API, used to fetch show/episode metadata that isn't available through
// Radio France's own (severely truncated) public RSS feeds.
package radiofrance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

const defaultBaseURL = "https://api.radiofrance.fr/v1/"

// Client calls the Radio France mobile API.
type Client struct {
	httpClient *http.Client
	baseURL    string
	token      string
}

// NewClient creates a Radio France API client. httpClient may be nil, in
// which case http.DefaultClient is used.
func NewClient(httpClient *http.Client, token string) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		httpClient: httpClient,
		baseURL:    defaultBaseURL,
		token:      token,
	}
}

// APIError is returned when the Radio France API responds with a non-2xx
// status.
type APIError struct {
	StatusCode int
	Path       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("radio france API error: %d (%s)", e.StatusCode, e.Path)
}

func (c *Client) doGet(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("building request for %s: %w", path, err)
	}
	req.Header.Set("Accept", "application/x.radiofrance.mobileapi+json")
	req.Header.Set("User-Agent", "AppRF")
	req.Header.Set("x-token", c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("radio france API request failed (%s): %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{StatusCode: resp.StatusCode, Path: path}
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding radio france API response (%s): %w", path, err)
	}
	return nil
}
