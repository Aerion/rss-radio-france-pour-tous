package radiofrance

import (
	"context"
	"fmt"
)

// GetDiffusion fetches a single diffusion by ID directly, bypassing the
// paginated shows/{id}/diffusions listing. Used to look up a rerun's origin
// diffusion (see Diffusion.OriginDiffusionID) when it isn't otherwise being
// paged through.
func (c *Client) GetDiffusion(ctx context.Context, diffusionID string) (Diffusion, error) {
	var resp diffusionResponse
	if err := c.doGet(ctx, "diffusion", fmt.Sprintf("diffusions/%s", diffusionID), &resp); err != nil {
		return Diffusion{}, err
	}
	return resp.Data.Diffusions, nil
}

// GetDiffusionManifestations fetches every manifestation inlined with a
// single diffusion via include=manifestations - the same JSON:API param
// getDiffusionsPage already uses on the plural shows/{id}/diffusions
// listing, applied here to the singular endpoint. Collapses what would
// otherwise be one GetManifestation call per sibling manifestation (see
// Diffusion.Relationships.Manifestations, typically ~8 of them) into a
// single request. Coverage isn't guaranteed exhaustive (same caveat as
// diffusionsResponse.Included.Manifestations) - callers should fall back to
// GetManifestation for whatever candidate IDs are still missing from the
// returned map.
func (c *Client) GetDiffusionManifestations(ctx context.Context, diffusionID string) (map[string]ManifestationDetails, error) {
	var resp diffusionManifestationsResponse
	path := fmt.Sprintf("diffusions/%s?include=manifestations", diffusionID)
	if err := c.doGet(ctx, "diffusion_manifestations", path, &resp); err != nil {
		return nil, err
	}

	manifestations := make(map[string]ManifestationDetails, len(resp.Included.Manifestations))
	for id, raw := range resp.Included.Manifestations {
		if details := raw.toDetails(); details.URL != "" {
			manifestations[id] = details
		}
	}
	return manifestations, nil
}
