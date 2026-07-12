package radiofrance

import (
	"context"
	"net/http"
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
