package radiofrance

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestSearch_ReturnsOnlyShowResults(t *testing.T) {
	client := newTestClient(t, serveFixture(t, "api-search-response.json"))

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
	if !strings.Contains(results[0].Path, "radiofrance.fr") {
		t.Errorf("results[0].Path = %q", results[0].Path)
	}
	if results[0].ShowID != "0b91efaf-26e6-11e4-907f-782bcb6744eb" {
		t.Errorf("results[0].ShowID = %q", results[0].ShowID)
	}
}

func TestSearch_EmptyResults(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data": [], "included": {"shows": {}}}`))
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
