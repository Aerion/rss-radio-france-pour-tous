package radiofrance

import (
	"context"
	"fmt"
)

// GetManifestationURL resolves a manifestation's real audio URL.
func (c *Client) GetManifestationURL(ctx context.Context, manifestationID string) (string, error) {
	var resp manifestationResponse
	if err := c.doGet(ctx, fmt.Sprintf("manifestations/%s", manifestationID), &resp); err != nil {
		return "", err
	}
	if resp.Data.Manifestations.URL == "" {
		return "", fmt.Errorf("no URL found for manifestation %s", manifestationID)
	}
	return resp.Data.Manifestations.URL, nil
}
