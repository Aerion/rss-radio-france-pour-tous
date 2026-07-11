package radiofrance

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestGetManifestation(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data": {"manifestations": {
			"url": "https://cdn.example.com/audio.mp3",
			"duration": 1731,
			"principal": true,
			"downloadExpirationDate": 1799103600
		}}}`))
	})

	details, err := client.GetManifestation(context.Background(), "301c6eb1-61d4-4120-8cd7-e415ffc4f7df")
	if err != nil {
		t.Fatalf("GetManifestation: %v", err)
	}
	if details.URL != "https://cdn.example.com/audio.mp3" {
		t.Errorf("URL = %q", details.URL)
	}
	if details.Duration != 1731*time.Second {
		t.Errorf("Duration = %v, want 1731s", details.Duration)
	}
	if !details.Principal {
		t.Error("Principal = false, want true")
	}
	if details.ExpiresAt == nil || details.ExpiresAt.Unix() != 1799103600 {
		t.Errorf("ExpiresAt = %v, want 1799103600", details.ExpiresAt)
	}
}

func TestGetManifestation_PrefersDownloadExpirationOverStream(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data": {"manifestations": {
			"url": "https://cdn.example.com/audio.mp3",
			"downloadExpirationDate": 100,
			"streamExpirationDate": 200
		}}}`))
	})

	details, err := client.GetManifestation(context.Background(), "id")
	if err != nil {
		t.Fatalf("GetManifestation: %v", err)
	}
	if details.ExpiresAt.Unix() != 100 {
		t.Errorf("ExpiresAt = %v, want 100", details.ExpiresAt)
	}
}

func TestGetManifestation_NoExpiration(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data": {"manifestations": {"url": "https://cdn.example.com/audio.mp3"}}}`))
	})

	details, err := client.GetManifestation(context.Background(), "id")
	if err != nil {
		t.Fatalf("GetManifestation: %v", err)
	}
	if details.ExpiresAt != nil {
		t.Errorf("ExpiresAt = %v, want nil", details.ExpiresAt)
	}
}

func TestGetManifestation_Missing(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data": {"manifestations": {}}}`))
	})

	_, err := client.GetManifestation(context.Background(), "nonexistent-id")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
}

func TestGetManifestation_UpstreamError(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	_, err := client.GetManifestation(context.Background(), "nonexistent-id")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
}
