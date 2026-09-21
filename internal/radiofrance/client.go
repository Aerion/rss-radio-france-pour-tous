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

	"golang.org/x/time/rate"
)

const defaultBaseURL = "https://api.radiofrance.fr/v1/"

// defaultSAPIKBaseURL is the base for SAPIK, the server-driven-UI backend
// the Radio France app renders its screens from. It is a different API to
// the v1 mobile one above - unauthenticated, and returning layout trees
// rather than resources - and only search goes through it (see Search).
//
// "app" is the platform segment, selecting the phone/tablet form factor;
// "auto" and "tv" return the same content as differently-shaped cards.
const defaultSAPIKBaseURL = "https://api.radiofrance.fr/sapik/app/"

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
	// ObserveRequestStarted is called immediately before a call is
	// dispatched, letting dashboards see calls as they're fired rather
	// than only once they complete (see ObserveRequest) - the gap between
	// the two is what surfaces calls that are hanging.
	ObserveRequestStarted(ctx context.Context, endpoint string)
	ObserveRequest(ctx context.Context, endpoint string, ok bool, duration time.Duration)
}

// Client calls the Radio France mobile API.
type Client struct {
	httpClient *http.Client
	baseURL    string
	// sapikBaseURL is the base for SAPIK calls; see defaultSAPIKBaseURL.
	sapikBaseURL string
	token        string
	// observer is nil-able; calls are simply unrecorded if it's nil.
	observer RequestObserver
	// limiter is nil-able; calls go out unthrottled if it's nil (as in
	// tests, which build a Client literal directly).
	limiter *rate.Limiter
}

// NewClient creates a Radio France API client. httpClient may be nil, in
// which case http.DefaultClient is used. observer may be nil to skip
// recording call metrics. requestsPerSecond caps the outbound rate to the
// Radio France API, with no burst allowance - applied before every call,
// success or failure, so a run of upstream errors can't turn into a faster
// hammering of an already-struggling API.
func NewClient(httpClient *http.Client, token string, observer RequestObserver, requestsPerSecond float64) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		httpClient:   httpClient,
		baseURL:      defaultBaseURL,
		sapikBaseURL: defaultSAPIKBaseURL,
		token:        token,
		observer:     observer,
		limiter:      rate.NewLimiter(rate.Limit(requestsPerSecond), 1),
	}
}

// APIError is returned when the Radio France API responds with a non-2xx
// status. Header and Body carry the actual response so it shows up in logs
// - Radio France's error pages usually explain the failure (rate limiting,
// WAF block, etc.) better than the status code alone.
type APIError struct {
	StatusCode int
	// URL is the full request URL, query string included.
	URL    string
	Header http.Header
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("radio france API error: %d (%s): headers=%v body=%q", e.StatusCode, e.URL, e.Header, e.Body)
}

// doGet fetches path and decodes the JSON response into out. endpoint is a
// short, low-cardinality label (e.g. "diffusions", "manifestation") used
// for call metrics - not the raw path, which contains IDs.
func (c *Client) doGet(ctx context.Context, endpoint, path string, out any) error {
	return c.observe(ctx, endpoint, func() error {
		return c.get(ctx, c.baseURL+path, c.setMobileAPIHeaders, out)
	})
}

// doGetSAPIK is doGet against the SAPIK backend, which needs its own base
// URL and headers (see setSAPIKHeaders). Calls are recorded under the same
// endpoint labels and pass through the same rate limiter, since both APIs
// sit behind api.radiofrance.fr.
func (c *Client) doGetSAPIK(ctx context.Context, endpoint, path string, out any) error {
	return c.observe(ctx, endpoint, func() error {
		return c.get(ctx, c.sapikBaseURL+path, setSAPIKHeaders, out)
	})
}

// observe runs call, reporting its timing and outcome to c.observer.
func (c *Client) observe(ctx context.Context, endpoint string, call func() error) error {
	if c.observer != nil {
		c.observer.ObserveRequestStarted(ctx, endpoint)
	}
	start := time.Now()
	err := call()
	if c.observer != nil {
		c.observer.ObserveRequest(ctx, endpoint, err == nil, time.Since(start))
	}
	return err
}

func (c *Client) setMobileAPIHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/x.radiofrance.mobileapi+json")
	req.Header.Set("User-Agent", "AppRF")
	req.Header.Set("x-token", c.token)
}

// setSAPIKHeaders mimics the Android app's Ktor client. SAPIK takes no
// token; it does look at the User-Agent, so this sends the app's own rather
// than identifying as something the backend has never seen.
func setSAPIKHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "com.radiofrance.radio.radiofrance.android/9.29-android (Android 14; samsung; SM-G991B)")
}

func (c *Client) get(ctx context.Context, url string, setHeaders func(*http.Request), out any) error {
	if c.limiter != nil {
		if err := c.limiter.Wait(ctx); err != nil {
			return fmt.Errorf("waiting for radio france API rate limiter (%s): %w", url, err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("building request for %s: %w", url, err)
	}
	setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("radio france API request failed (%s): %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
		return &APIError{StatusCode: resp.StatusCode, URL: url, Header: resp.Header, Body: string(body)}
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding radio france API response (%s): %w", url, err)
	}
	return nil
}
