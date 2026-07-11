package httpapi

import (
	"context"

	"github.com/Aerion/rss-radio-france-pour-tous/internal/radiofrance"
)

// fakeAPI is a test double for API, avoiding any real HTTP round-trip to
// Radio France - mirroring the mocking boundary the original Vitest suite
// used (stubbing the global fetch).
type fakeAPI struct {
	showDiffusions    radiofrance.ShowDiffusions
	showDiffusionsErr error

	searchResults []radiofrance.SearchResult
	searchErr     error
}

func (f *fakeAPI) GetShowDiffusions(ctx context.Context, showID string, page int) (radiofrance.ShowDiffusions, error) {
	return f.showDiffusions, f.showDiffusionsErr
}

func (f *fakeAPI) Search(ctx context.Context, query string) ([]radiofrance.SearchResult, error) {
	return f.searchResults, f.searchErr
}

// fakeAudioResolver is a test double for AudioResolver.
type fakeAudioResolver struct {
	url       string
	showID    string
	showTitle string
	err       error
}

func (f *fakeAudioResolver) ResolveAudioURL(ctx context.Context, manifestationID string) (url, showID, showTitle string, err error) {
	return f.url, f.showID, f.showTitle, f.err
}
