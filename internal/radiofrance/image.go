package radiofrance

import "fmt"

// ImageURL picks a cover image for a show or diffusion and returns the URL
// to fetch it from, preferring (in order) a "square_banner" visual, a
// "square_visual" visual, the first available visual, and finally
// fallbackID. Returns "" if no image is available.
func ImageURL(visuals []Visual, fallbackID string) string {
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

	id := fallbackID
	switch {
	case len(visuals) == 0:
		id = fallbackID
	case squareBanner != "":
		id = squareBanner
	case squareVisual != "":
		id = squareVisual
	default:
		id = first
	}

	if id == "" {
		return ""
	}
	return fmt.Sprintf("https://api.radiofrance.fr/v1/services/embed/image/%s?preset=568x568", id)
}
