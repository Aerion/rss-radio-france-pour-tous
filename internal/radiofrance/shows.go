package radiofrance

import (
	"context"
	"fmt"
)

// GetShowDiffusions fetches a single page of a show's episode list. page is
// clamped to 0 (the first page) if negative.
func (c *Client) GetShowDiffusions(ctx context.Context, showID string, page int) (ShowDiffusions, error) {
	if page < 0 {
		page = 0
	}

	resp, err := c.getDiffusionsPage(ctx, showID, page)
	if err != nil {
		return ShowDiffusions{}, err
	}

	showDetails, ok := resp.Included.Shows[showID]
	if !ok {
		// For some shows, the Radio France API omits the show from
		// `included.shows` for reasons unknown. Fall back to fetching it
		// directly.
		showDetails, err = c.getShowDetails(ctx, showID)
		if err != nil {
			return ShowDiffusions{}, err
		}
	}

	diffusions := make([]Diffusion, 0, len(resp.Data))
	for _, item := range resp.Data {
		diffusions = append(diffusions, item.Diffusions)
	}

	manifestations := toManifestationDetails(resp.Included.Manifestations)

	var nextPageIdx *int
	if resp.Links.Next != "" {
		next := page + 1
		nextPageIdx = &next
	}

	return ShowDiffusions{
		Diffusions:     diffusions,
		ShowDetails:    showDetails,
		Manifestations: manifestations,
		NextPageIdx:    nextPageIdx,
	}, nil
}

func (c *Client) getDiffusionsPage(ctx context.Context, showID string, page int) (diffusionsResponse, error) {
	// include=manifestations pulls manifestation details inline (see
	// diffusionsResponse.Included.Manifestations), letting most episodes on
	// a page resolve their audio/duration without a separate per-episode
	// call - coverage isn't exhaustive, so callers still need a fallback.
	path := fmt.Sprintf(
		"shows/%s/diffusions?filter[manifestations][exists]=true&include=show&include=manifestations&page[offset]=%d",
		showID, page,
	)
	var resp diffusionsResponse
	if err := c.doGet(ctx, "diffusions", path, &resp); err != nil {
		return diffusionsResponse{}, err
	}
	return resp, nil
}

func (c *Client) getShowDetails(ctx context.Context, showID string) (Show, error) {
	var resp showResponse
	if err := c.doGet(ctx, "show_details", fmt.Sprintf("shows/%s", showID), &resp); err != nil {
		return Show{}, err
	}
	return resp.Data.Shows, nil
}
