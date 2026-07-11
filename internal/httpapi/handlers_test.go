package httpapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Aerion/rss-radio-france-pour-tous/internal/radiofrance"
)

func newTestServer(t *testing.T, api API) http.Handler {
	t.Helper()
	return NewServer(api, "https://radio-france-rss.example.com").Routes()
}

func TestHandleRequest_UnknownRoute404(t *testing.T) {
	h := newTestServer(t, &fakeAPI{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/unknown", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestHandleRequest_Robots(t *testing.T) {
	h := newTestServer(t, &fakeAPI{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/robots.txt", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Errorf("Content-Type = %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Disallow: /rss/") || !strings.Contains(body, "Disallow: /audio/") {
		t.Errorf("body = %q", body)
	}
}

func TestHandleRequest_Homepage(t *testing.T) {
	h := newTestServer(t, &fakeAPI{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "RSS Radio France pour tous") {
		t.Error("homepage body missing expected title")
	}
}

func TestHandleRequest_RSSFeed(t *testing.T) {
	show := radiofrance.Show{ID: "0b91efaf", Title: "Affaires sensibles"}
	d := radiofrance.Diffusion{ID: "d1", Title: "Episode 1", CreatedTime: 1700000000}
	d.Relationships.Manifestations = []string{"m1"}

	api := &fakeAPI{showDiffusions: radiofrance.ShowDiffusions{
		Diffusions:  []radiofrance.Diffusion{d},
		ShowDetails: show,
	}}
	h := newTestServer(t, api)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/rss/0b91efaf", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/xml") {
		t.Errorf("Content-Type = %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<rss") || !strings.Contains(body, "Affaires sensibles") {
		t.Errorf("body = %q", body)
	}
}

func TestHandleRequest_RSSFeed_UpstreamError(t *testing.T) {
	api := &fakeAPI{showDiffusionsErr: errors.New("upstream boom")}
	h := newTestServer(t, api)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/rss/0b91efaf", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestHandleRequest_Search(t *testing.T) {
	api := &fakeAPI{searchResults: []radiofrance.SearchResult{
		{ShowID: "0b91efaf", Title: "Affaires sensibles", Path: "https://radiofrance.fr/affaires-sensibles"},
	}}
	h := newTestServer(t, api)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/search/?query=affaires+sensibles", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Affaires sensibles") || !strings.Contains(body, `"rssUrl"`) {
		t.Errorf("body = %q", body)
	}
}

func TestHandleRequest_Search_MissingQuery(t *testing.T) {
	h := newTestServer(t, &fakeAPI{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/search/", nil))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleRequest_Audio(t *testing.T) {
	api := &fakeAPI{manifestationURL: "https://cdn.example.com/audio.mp3"}
	h := newTestServer(t, api)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/audio/301c6eb1-61d4-4120-8cd7-e415ffc4f7df", nil))

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "https://cdn.example.com/audio.mp3" {
		t.Errorf("Location = %q", loc)
	}
}

func TestHandleRequest_Audio_UpstreamError(t *testing.T) {
	api := &fakeAPI{manifestationErr: errors.New("not found upstream")}
	h := newTestServer(t, api)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/audio/nonexistent-id", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}
