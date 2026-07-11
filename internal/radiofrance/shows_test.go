package radiofrance

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestGetShowDiffusions_ShowDetailsFromIncluded(t *testing.T) {
	client := newTestClient(t, serveFixture(t, "api-show-0b91efaf.json"))

	got, err := client.GetShowDiffusions(context.Background(), "0b91efaf-26e6-11e4-907f-782bcb6744eb", 0)
	if err != nil {
		t.Fatalf("GetShowDiffusions: %v", err)
	}

	if got.ShowDetails.Title != "Affaires sensibles" {
		t.Errorf("ShowDetails.Title = %q", got.ShowDetails.Title)
	}
	if len(got.Diffusions) != 100 {
		t.Errorf("len(Diffusions) = %d, want 100", len(got.Diffusions))
	}
	if got.NextPageIdx == nil || *got.NextPageIdx != 1 {
		t.Errorf("NextPageIdx = %v, want pointer to 1", got.NextPageIdx)
	}
}

func TestGetShowDiffusions_PopulatesManifestationsFromIncluded(t *testing.T) {
	client := newTestClient(t, serveFixture(t, "api-show-0b91efaf.json"))

	got, err := client.GetShowDiffusions(context.Background(), "0b91efaf-26e6-11e4-907f-782bcb6744eb", 0)
	if err != nil {
		t.Fatalf("GetShowDiffusions: %v", err)
	}

	if len(got.Manifestations) == 0 {
		t.Fatal("expected at least one manifestation from included.manifestations")
	}

	// Cross-check: every diffusion's ManifestationID(), if present in the
	// included map, should have a usable URL and a real Duration.
	foundPrincipal := false
	for _, d := range got.Diffusions {
		for _, id := range d.Relationships.Manifestations {
			m, ok := got.Manifestations[id]
			if !ok {
				continue
			}
			if m.URL == "" {
				t.Errorf("manifestation %s has an empty URL", id)
			}
			if m.Principal {
				foundPrincipal = true
			}
		}
	}
	if !foundPrincipal {
		t.Error("expected at least one included manifestation flagged Principal across the page")
	}
}

func TestGetShowDiffusions_RequestsManifestationsInline(t *testing.T) {
	var gotQuery string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Write(readTestdata(t, "api-show-0b91efaf.json"))
	})

	_, err := client.GetShowDiffusions(context.Background(), "0b91efaf-26e6-11e4-907f-782bcb6744eb", 0)
	if err != nil {
		t.Fatalf("GetShowDiffusions: %v", err)
	}
	if !strings.Contains(gotQuery, "include=manifestations") {
		t.Errorf("query = %q, want it to contain include=manifestations", gotQuery)
	}
}

func TestGetShowDiffusions_NoNextPage(t *testing.T) {
	client := newTestClient(t, serveFixture(t, "api-show-4a41823f.json"))

	got, err := client.GetShowDiffusions(context.Background(), "4a41823f-f1f7-4725-8380-e428893eb93b", 0)
	if err != nil {
		t.Fatalf("GetShowDiffusions: %v", err)
	}
	if got.NextPageIdx != nil {
		t.Errorf("NextPageIdx = %v, want nil", *got.NextPageIdx)
	}
}

func TestGetShowDiffusions_FallsBackToShowDetailsEndpoint(t *testing.T) {
	diffusionsBody := readTestdata(t, "api-show-70b2e0a9.json")
	showBody := readTestdata(t, "api-show-70b2e0a9-details.json")

	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/diffusions") {
			w.Write(diffusionsBody)
			return
		}
		w.Write(showBody)
	})

	got, err := client.GetShowDiffusions(context.Background(), "70b2e0a9-4722-4291-932e-555eff12239e", 0)
	if err != nil {
		t.Fatalf("GetShowDiffusions: %v", err)
	}
	if !strings.Contains(got.ShowDetails.Title, "Amie prodigieuse") {
		t.Errorf("ShowDetails.Title = %q", got.ShowDetails.Title)
	}
	if len(got.Diffusions) == 0 {
		t.Error("expected at least one diffusion")
	}
}

func TestGetShowDiffusions_UpstreamError(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	_, err := client.GetShowDiffusions(context.Background(), "0b91efaf-26e6-11e4-907f-782bcb6744eb", 0)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
}

func TestGetShowDiffusions_NegativePageClampedToFirstPage(t *testing.T) {
	var gotOffset string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotOffset = r.URL.Query().Get("page[offset]")
		w.Write(readTestdata(t, "api-show-4a41823f.json"))
	})

	_, err := client.GetShowDiffusions(context.Background(), "4a41823f-f1f7-4725-8380-e428893eb93b", -1)
	if err != nil {
		t.Fatalf("GetShowDiffusions: %v", err)
	}
	if gotOffset != "0" {
		t.Errorf("page[offset] = %q, want %q (negative page should be clamped to the first page)", gotOffset, "0")
	}
}
