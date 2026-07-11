package radiofrance

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
)

// Search looks up shows matching a free-text query.
func (c *Client) Search(ctx context.Context, query string) ([]SearchResult, error) {
	path := fmt.Sprintf("stations/search?value=%s&include=show", url.QueryEscape(query))

	var resp searchResponse
	if err := c.doGet(ctx, path, &resp); err != nil {
		return nil, err
	}

	results := make([]SearchResult, 0, len(resp.Data))
	for _, item := range resp.Data {
		if item.ResultItems.Model != "show" {
			continue
		}
		showIDs := item.ResultItems.Relationships.Show
		if len(showIDs) == 0 {
			continue
		}
		show, ok := resp.Included.Shows[showIDs[0]]
		if !ok {
			slog.Warn("search result references a show missing from included data", "showID", showIDs[0])
			continue
		}
		results = append(results, SearchResult{
			ShowID:     show.ID,
			Title:      show.Title,
			Path:       show.Path,
			Standfirst: show.Standfirst,
			ImgURL:     ImageURL(show.Visuals, show.MainImage),
		})
	}
	return results, nil
}
