package analytics

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestWriter builds a Writer with no worker goroutines (workers=0), so
// tests can inspect enqueued events directly from the channel without a
// real database.
func newTestWriter(t *testing.T, bufferSize int) *Writer {
	t.Helper()
	return NewWriter(nil, nil, bufferSize, 0)
}

func TestWrap_EnqueuesEventWithRequestDetails(t *testing.T) {
	w := newTestWriter(t, 1)
	handler := w.Wrap("rss", func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusNotFound)
	})

	req := httptest.NewRequest(http.MethodGet, "/rss/some-show", nil)
	req.Header.Set("User-Agent", "AntennaPod/3.0")
	handler(httptest.NewRecorder(), req)

	select {
	case e := <-w.events:
		if e.route != "rss" {
			t.Errorf("route = %q, want rss", e.route)
		}
		if e.method != http.MethodGet {
			t.Errorf("method = %q, want GET", e.method)
		}
		if e.status != http.StatusNotFound {
			t.Errorf("status = %d, want 404", e.status)
		}
		if e.userAgent != "AntennaPod/3.0" {
			t.Errorf("userAgent = %q, want AntennaPod/3.0", e.userAgent)
		}
	default:
		t.Fatal("expected an event to be enqueued")
	}
}

func TestWrap_CapturesShowInfoFromWithShow(t *testing.T) {
	w := newTestWriter(t, 1)
	handler := w.Wrap("rss", func(rw http.ResponseWriter, r *http.Request) {
		WithShow(r.Context(), "0b91efaf-26e6-11e4-907f-782bcb6744eb", "Affaires sensibles")
	})

	req := httptest.NewRequest(http.MethodGet, "/rss/0b91efaf", nil)
	handler(httptest.NewRecorder(), req)

	e := <-w.events
	if e.showID != "0b91efaf-26e6-11e4-907f-782bcb6744eb" {
		t.Errorf("showID = %q", e.showID)
	}
	if e.showTitle != "Affaires sensibles" {
		t.Errorf("showTitle = %q", e.showTitle)
	}
}

func TestWrap_DropsEventWhenBufferFull(t *testing.T) {
	w := newTestWriter(t, 1)
	handler := w.Wrap("rss", func(rw http.ResponseWriter, r *http.Request) {})

	req := httptest.NewRequest(http.MethodGet, "/rss/some-show", nil)

	// Fill the buffer (size 1), then send a second request that should be
	// dropped rather than blocking.
	handler(httptest.NewRecorder(), req)

	done := make(chan struct{})
	go func() {
		handler(httptest.NewRecorder(), req)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Wrap blocked instead of dropping the event when the buffer was full")
	}

	if len(w.events) != 1 {
		t.Errorf("len(events) = %d, want 1 (buffer full, second event dropped)", len(w.events))
	}
}
