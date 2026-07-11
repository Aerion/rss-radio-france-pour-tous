package radiofrance

import (
	"context"
	"fmt"
	"time"
)

// GetManifestation resolves a manifestation's playback details.
func (c *Client) GetManifestation(ctx context.Context, manifestationID string) (ManifestationDetails, error) {
	var resp manifestationResponse
	if err := c.doGet(ctx, "manifestation", fmt.Sprintf("manifestations/%s", manifestationID), &resp); err != nil {
		return ManifestationDetails{}, err
	}

	m := resp.Data.Manifestations
	if m.URL == "" {
		return ManifestationDetails{}, fmt.Errorf("no URL found for manifestation %s", manifestationID)
	}

	details := ManifestationDetails{
		URL:       m.URL,
		Duration:  time.Duration(m.Duration) * time.Second,
		Principal: m.Principal,
	}
	switch {
	case m.DownloadExpirationDate != nil:
		t := time.Unix(*m.DownloadExpirationDate, 0)
		details.ExpiresAt = &t
	case m.StreamExpirationDate != nil:
		t := time.Unix(*m.StreamExpirationDate, 0)
		details.ExpiresAt = &t
	}
	return details, nil
}
