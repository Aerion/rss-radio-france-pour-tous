package radiofrance

import (
	"context"
	"net/url"
)

// searchEntitiesPodcasts is the SAPIK search filter that restricts results
// to podcasts, dropping the episode and personality sections the app shows
// alongside them. The other values the app's filter sheet offers are
// EXPRESSIONS_ARTICLES (episodes) and PERSONALITIES; several can be
// comma-joined.
//
// SAPIK ignores a value it doesn't recognise and answers with every
// section instead of erroring, so this is a narrowing hint, not a
// guarantee - hence the shimmer check in Search below.
const searchEntitiesPodcasts = "CONCEPTS"

// Search looks up shows matching a free-text query.
//
// This goes to SAPIK rather than the v1 mobile API's stations/search: that
// endpoint spends upwards of 10s on queries made of common words ("Pour qui
// travaille France Travail"), past which the gateway gives up and returns a
// cached 503, so a long podcast title would reliably find nothing. SAPIK is
// the backend the app's own search screen uses, answers the same query in
// under 2s, and ranks the exact title first.
//
// SAPIK caps each section at 20 results and offers no pagination, so that
// is the ceiling on what this returns.
func (c *Client) Search(ctx context.Context, query string) ([]SearchResult, error) {
	params := url.Values{
		"entities": {searchEntitiesPodcasts},
		"query":    {query},
	}
	path := "layout/search?" + params.Encode()

	var resp sapikSearchResponse
	if err := c.doGetSAPIK(ctx, "search", path, &resp); err != nil {
		return nil, err
	}

	results := make([]SearchResult, 0, 20)
	seen := make(map[string]bool)
	for _, highlight := range resp.Highlights {
		for _, item := range highlight.Items {
			widget := item.Widget
			action := widget.Action
			// shimmer names the screen the card opens, and is what
			// separates a podcast from an episode or a personality -
			// more reliable than the section heading, and the check
			// that keeps non-podcasts out should the entities filter
			// above stop being honoured. The same podcast can appear
			// in more than one section, so dedupe on the way out.
			if action.Type != "openLayout" || action.Shimmer.ID != "concept" || action.ID == "" {
				continue
			}
			if seen[action.ID] {
				continue
			}
			seen[action.ID] = true

			results = append(results, SearchResult{
				ShowID: action.ID,
				Title:  widget.Titles.text(),
				ImgURL: buildImageURL(widget.Image.ID),
			})
		}
	}
	return results, nil
}
