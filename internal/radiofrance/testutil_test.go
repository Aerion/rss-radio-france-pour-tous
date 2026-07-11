package radiofrance

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading testdata/%s: %v", name, err)
	}
	return b
}

// newTestClient starts an httptest server driven by handler and returns a
// Client pointed at it.
func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	return &Client{
		httpClient: server.Client(),
		baseURL:    server.URL + "/",
		token:      "test-token",
	}
}

// serveFixture returns a handler that always responds with the given
// testdata file's contents.
func serveFixture(t *testing.T, name string) http.HandlerFunc {
	t.Helper()
	body := readTestdata(t, name)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}
}
