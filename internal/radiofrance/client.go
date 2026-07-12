// Package radiofrance is a client for Radio France's unofficial mobile-app
// API, used to fetch show/episode metadata that isn't available through
// Radio France's own (severely truncated) public RSS feeds.
package radiofrance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const defaultBaseURL = "https://api.radiofrance.fr/v1/"

// maxErrorBodySize caps how much of a non-2xx response body APIError
// captures, so an unexpected large error page (e.g. an HTML WAF block page)
// can't bloat logs.
const maxErrorBodySize = 4 << 10

// RequestObserver receives timing/outcome for each outbound Radio France
// API call. Defined here rather than in a metrics package so this package
// stays decoupled from any particular metrics backend;
// observability.Observability implements it. ctx is passed through (rather
// than just the call's own outcome/duration) so an implementation can
// correlate the recorded metric with the current request.
type RequestObserver interface {
	ObserveRequest(ctx context.Context, endpoint string, ok bool, duration time.Duration)
}

// Client calls the Radio France mobile API.
type Client struct {
	httpClient *http.Client
	baseURL    string
	token      string
	// observer is nil-able; calls are simply unrecorded if it's nil.
	observer RequestObserver
}

// NewClient creates a Radio France API client. httpClient may be nil, in
// which case http.DefaultClient is used. observer may be nil to skip
// recording call metrics.
func NewClient(httpClient *http.Client, token string, observer RequestObserver) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		httpClient: httpClient,
		baseURL:    defaultBaseURL,
		token:      token,
		observer:   observer,
	}
}

// APIError is returned when the Radio France API responds with a non-2xx
// status. Header and Body carry the actual response so it shows up in logs
// - Radio France's error pages usually explain the failure (rate limiting,
// WAF block, etc.) better than the status code alone.
type APIError struct {
	StatusCode int
	Path       string
	Header     http.Header
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("radio france API error: %d (%s): headers=%v body=%q", e.StatusCode, e.Path, e.Header, e.Body)
}

// doGet fetches path and decodes the JSON response into out. endpoint is a
// short, low-cardinality label (e.g. "diffusions", "manifestation") used
// for call metrics - not the raw path, which contains IDs.
func (c *Client) doGet(ctx context.Context, endpoint, path string, out any) error {
	start := time.Now()
	err := c.get(ctx, path, out)
	if c.observer != nil {
		c.observer.ObserveRequest(ctx, endpoint, err == nil, time.Since(start))
	}
	return err
}

func (c *Client) get(ctx context.Context, path string, out any) error {
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
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
		return &APIError{StatusCode: resp.StatusCode, Path: path, Header: resp.Header, Body: string(body)}
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding radio france API response (%s): %w", path, err)
	}
	return nil
}
