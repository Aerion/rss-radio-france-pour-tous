package radiofrance

import (
	"context"
	"fmt"
)

// GetManifestation resolves a manifestation's playback details.
func (c *Client) GetManifestation(ctx context.Context, manifestationID string) (ManifestationDetails, error) {
	var resp manifestationResponse
	if err := c.doGet(ctx, "manifestation", fmt.Sprintf("manifestations/%s", manifestationID), &resp); err != nil {
		return ManifestationDetails{}, err
	}

	details := resp.Data.Manifestations.toDetails()
	if details.URL == "" {
		return ManifestationDetails{}, fmt.Errorf("no URL found for manifestation %s", manifestationID)
	}
	return details, nil
}
