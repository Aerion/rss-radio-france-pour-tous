package radiofrance

import (
	"context"
	"net/http"
	"testing"
)

func TestGetManifestationURL(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data": {"manifestations": {"url": "https://cdn.example.com/audio.mp3", "id": "some-id"}}}`))
	})

	url, err := client.GetManifestationURL(context.Background(), "301c6eb1-61d4-4120-8cd7-e415ffc4f7df")
	if err != nil {
		t.Fatalf("GetManifestationURL: %v", err)
	}
	if url != "https://cdn.example.com/audio.mp3" {
		t.Errorf("url = %q", url)
	}
}

func TestGetManifestationURL_Missing(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data": {"manifestations": {}}}`))
	})

	_, err := client.GetManifestationURL(context.Background(), "nonexistent-id")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
}

func TestGetManifestationURL_UpstreamError(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	_, err := client.GetManifestationURL(context.Background(), "nonexistent-id")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
}
