package radiofrance

import "fmt"

// ImageURL picks a cover image for a show and returns the URL to fetch it
// from, preferring (in order) a "square_banner" visual, a "square_visual"
// visual, the first available visual, and finally fallbackID. Returns "" if
// no image is available.
func ImageURL(visuals []Visual, fallbackID string) string {
	id := pickVisual(visuals)
	if id == "" {
		id = fallbackID
	}
	return buildImageURL(id)
}

// DiffusionImageURL picks a cover image for a single episode. Unlike
// ImageURL, it prefers d's own MainImage over its Visuals: live API samples
// show a diffusion's visuals (its "square_banner" in particular) are often
// just the enclosing season/show's shared banner, repeated identically
// across every episode in that season, whereas MainImage is the actual
// per-episode artwork when one exists. Falls back to the visuals-based
// selection when MainImage is absent, so episodes without their own art
// still get the best available image rather than none. Returns "" if
// neither is available.
func DiffusionImageURL(d Diffusion) string {
	id := d.MainImage
	if id == "" {
		id = pickVisual(d.Visuals)
	}
	return buildImageURL(id)
}

// pickVisual returns the best visual_uuid among visuals, preferring (in
// order) "square_banner", "square_visual", then the first entry. Returns ""
// if visuals is empty.
func pickVisual(visuals []Visual) string {
	var squareBanner, squareVisual, first string
	for i, v := range visuals {
		if i == 0 {
			first = v.VisualUUID
		}
		switch v.Name {
		case "square_banner":
			squareBanner = v.VisualUUID
		case "square_visual":
			squareVisual = v.VisualUUID
		}
	}

	switch {
	case squareBanner != "":
		return squareBanner
	case squareVisual != "":
		return squareVisual
	default:
		return first
	}
}

// buildImageURL returns the Radio France image-embed URL for visual id, or
// "" if id is empty.
func buildImageURL(id string) string {
	if id == "" {
		return ""
	}
	return fmt.Sprintf("https://api.radiofrance.fr/v1/services/embed/image/%s?preset=568x568", id)
}
