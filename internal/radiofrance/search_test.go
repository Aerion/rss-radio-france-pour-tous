package radiofrance

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestSearch_ReturnsPodcastResults(t *testing.T) {
	client := newTestClient(t, serveFixture(t, "api-sapik-search-response.json"))

	results, err := client.Search(context.Background(), "affaires sensibles")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	if results[0].Title != "Affaires sensibles" {
		t.Errorf("results[0].Title = %q", results[0].Title)
	}
	if results[0].ShowID != "0b91efaf-26e6-11e4-907f-782bcb6744eb" {
		t.Errorf("results[0].ShowID = %q", results[0].ShowID)
	}
	if !strings.Contains(results[0].ImgURL, "7b50cf5f-f5bd-4dc4-8b1d-b08666768dcf") {
		t.Errorf("results[0].ImgURL = %q, want it built from the card's image id", results[0].ImgURL)
	}
}

// The entities=CONCEPTS filter asks the API for podcasts only, but an
// unrecognised value is silently ignored rather than rejected (and the
// filter vocabulary is the server's, not ours), so Search must not depend
// on it having been honoured.
func TestSearch_SkipsNonPodcastResults(t *testing.T) {
	client := newTestClient(t, serveFixture(t, "api-sapik-search-mixed-response.json"))

	results, err := client.Search(context.Background(), "affaires sensibles")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2 (the podcast cards; the fixture also holds 2 episode cards)", len(results))
	}
	for _, res := range results {
		if res.ShowID == "" || res.Title == "" {
			t.Errorf("incomplete result: %+v", res)
		}
	}
}

func TestSearch_RequestsPodcastsOnly(t *testing.T) {
	var got url.Values
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		_, _ = w.Write([]byte(`{"highlights": []}`))
	})

	if _, err := client.Search(context.Background(), "affaires sensibles"); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if q := got.Get("query"); q != "affaires sensibles" {
		t.Errorf("query = %q", q)
	}
	if e := got.Get("entities"); e != "CONCEPTS" {
		t.Errorf("entities = %q, want CONCEPTS", e)
	}
}

// A show can be listed in more than one section of the same response (a
// "recent searches" section repeating a podcast that also matched, say).
func TestSearch_DeduplicatesShows(t *testing.T) {
	const body = `{"highlights": [
	  {"items": [{"widget": {"titles": {"title": "Affaires sensibles"},
	    "action": {"type": "openLayout", "id": "show-1", "shimmer": {"id": "concept"}}}}]},
	  {"items": [{"widget": {"titles": {"title": "Affaires sensibles"},
	    "action": {"type": "openLayout", "id": "show-1", "shimmer": {"id": "concept"}}}}]}]}`

	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	})

	results, err := client.Search(context.Background(), "affaires sensibles")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("len(results) = %d, want 1", len(results))
	}
}

// SAPIK sends a card's title either bare or wrapped in a styling object,
// and only the latter on some platform segments - see sapikText.
func TestSearch_ReadsWrappedTitle(t *testing.T) {
	const body = `{"highlights": [{"items": [{"widget": {
	  "titles": {"titleText": {"style": "titleMedium", "label": "Affaires sensibles"}},
	  "action": {"type": "openLayout", "id": "show-1", "shimmer": {"id": "concept"}}}}]}]}`

	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	})

	results, err := client.Search(context.Background(), "affaires sensibles")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].Title != "Affaires sensibles" {
		t.Errorf("results = %+v", results)
	}
}

func TestSearch_EmptyResults(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"highlights": []}`))
	})

	results, err := client.Search(context.Background(), "rien")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("len(results) = %d, want 0", len(results))
	}
}

func TestSearch_UpstreamError(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	_, err := client.Search(context.Background(), "test")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
}
