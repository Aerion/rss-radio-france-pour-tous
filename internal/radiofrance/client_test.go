package radiofrance

import (
	"context"
	"net/http"
	"testing"
)

func TestDoGet_SetsRequiredHeaders(t *testing.T) {
	var gotAccept, gotUserAgent, gotToken string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		gotUserAgent = r.Header.Get("User-Agent")
		gotToken = r.Header.Get("x-token")
		w.Write([]byte(`{}`))
	})

	if err := client.doGet(context.Background(), "anything", &struct{}{}); err != nil {
		t.Fatalf("doGet returned error: %v", err)
	}

	if gotAccept != "application/x.radiofrance.mobileapi+json" {
		t.Errorf("Accept header = %q", gotAccept)
	}
	if gotUserAgent != "AppRF" {
		t.Errorf("User-Agent header = %q", gotUserAgent)
	}
	if gotToken != "test-token" {
		t.Errorf("x-token header = %q", gotToken)
	}
}

func TestDoGet_ErrorStatus(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	err := client.doGet(context.Background(), "anything", &struct{}{})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusServiceUnavailable)
	}
}
