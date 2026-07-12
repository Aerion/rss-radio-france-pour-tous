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
