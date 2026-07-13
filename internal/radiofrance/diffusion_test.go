package radiofrance

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestGetDiffusion(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/diffusions/origin-id" {
			t.Errorf("request path = %q, want /diffusions/origin-id", r.URL.Path)
		}
		w.Write([]byte(`{"data": {"diffusions": {
			"id": "origin-id",
			"title": "Original broadcast",
			"mainImage": "uuid-episode"
		}}}`))
	})

	d, err := client.GetDiffusion(context.Background(), "origin-id")
	if err != nil {
		t.Fatalf("GetDiffusion: %v", err)
	}
	if d.ID != "origin-id" {
		t.Errorf("ID = %q", d.ID)
	}
	if d.MainImage != "uuid-episode" {
		t.Errorf("MainImage = %q", d.MainImage)
	}
}

func TestGetDiffusion_UpstreamError(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	_, err := client.GetDiffusion(context.Background(), "nonexistent-id")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
}

func TestGetDiffusionManifestations(t *testing.T) {
	var gotQuery string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Write([]byte(`{"data": {"diffusions": {"id": "d1"}}, "included": {"manifestations": {
			"m1": {"url": "https://cdn.example.com/m1.mp3", "duration": 90, "principal": false},
			"m2": {"url": "https://cdn.example.com/m2.mp3", "duration": 91, "principal": true},
			"m3": {}
		}}}`))
	})

	got, err := client.GetDiffusionManifestations(context.Background(), "d1")
	if err != nil {
		t.Fatalf("GetDiffusionManifestations: %v", err)
	}
	if !strings.Contains(gotQuery, "include=manifestations") {
		t.Errorf("query = %q, want it to contain include=manifestations", gotQuery)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2 (m3 has no URL and should be dropped)", len(got))
	}
	if !got["m2"].Principal {
		t.Error("expected m2 to be flagged Principal")
	}
	if got["m1"].URL != "https://cdn.example.com/m1.mp3" {
		t.Errorf("m1 URL = %q", got["m1"].URL)
	}
}

func TestGetDiffusionManifestations_UpstreamError(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	_, err := client.GetDiffusionManifestations(context.Background(), "d1")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
}
