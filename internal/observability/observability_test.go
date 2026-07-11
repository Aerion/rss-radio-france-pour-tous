package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestObservability(t *testing.T) *Observability {
	t.Helper()
	o, err := New("test-service")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = o.Shutdown(ctx)
	})
	return o
}

func TestWrap_RecordsRequestMetrics(t *testing.T) {
	o := newTestObservability(t)
	handler := o.Wrap("rss", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/rss/some-show", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}

	body := scrapeMetrics(t, o)
	if !strings.Contains(body, `http_requests_total{method="GET",route="rss",status="404"} 1`) {
		t.Errorf("expected http_requests_total counter for rss/GET/404, got:\n%s", body)
	}
}

func TestWrap_DefaultsToStatus200WhenNotSetExplicitly(t *testing.T) {
	o := newTestObservability(t)
	handler := o.Wrap("home", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok")) // no explicit WriteHeader call
	})

	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	body := scrapeMetrics(t, o)
	if !strings.Contains(body, `http_requests_total{method="GET",route="home",status="200"} 1`) {
		t.Errorf("expected http_requests_total counter for home/GET/200, got:\n%s", body)
	}
}

func TestObserveRequest_RecordsRadioFranceMetrics(t *testing.T) {
	o := newTestObservability(t)
	ctx := context.Background()
	o.ObserveRequest(ctx, "diffusions", true, 42*time.Millisecond)
	o.ObserveRequest(ctx, "diffusions", false, 10*time.Millisecond)

	body := scrapeMetrics(t, o)
	if !strings.Contains(body, `radiofrance_api_requests_total{endpoint="diffusions",status="ok"} 1`) {
		t.Errorf("expected an ok counter for diffusions, got:\n%s", body)
	}
	if !strings.Contains(body, `radiofrance_api_requests_total{endpoint="diffusions",status="error"} 1`) {
		t.Errorf("expected an error counter for diffusions, got:\n%s", body)
	}
}

func scrapeMetrics(t *testing.T, o *Observability) string {
	t.Helper()
	rec := httptest.NewRecorder()
	o.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return rec.Body.String()
}
