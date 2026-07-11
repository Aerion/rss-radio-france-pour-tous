package radiofrance

import (
	"strings"
	"testing"
)

func TestImageURL_NoVisualsNoFallback(t *testing.T) {
	if got := ImageURL(nil, ""); got != "" {
		t.Errorf("ImageURL(nil, \"\") = %q, want \"\"", got)
	}
	if got := ImageURL([]Visual{}, ""); got != "" {
		t.Errorf("ImageURL([], \"\") = %q, want \"\"", got)
	}
}

func TestImageURL_UsesFallbackWhenNoVisuals(t *testing.T) {
	got := ImageURL(nil, "fallback-uuid")
	want := "https://api.radiofrance.fr/v1/services/embed/image/fallback-uuid?preset=568x568"
	if got != want {
		t.Errorf("ImageURL = %q, want %q", got, want)
	}
}

func TestImageURL_PrefersSquareBanner(t *testing.T) {
	visuals := []Visual{
		{Name: "square_visual", VisualUUID: "uuid-square"},
		{Name: "square_banner", VisualUUID: "uuid-banner"},
	}
	got := ImageURL(visuals, "")
	if !strings.Contains(got, "uuid-banner") {
		t.Errorf("ImageURL = %q, want it to contain uuid-banner", got)
	}
}

func TestImageURL_FallsBackToSquareVisual(t *testing.T) {
	visuals := []Visual{
		{Name: "square_visual", VisualUUID: "uuid-square"},
		{Name: "other", VisualUUID: "uuid-other"},
	}
	got := ImageURL(visuals, "")
	if !strings.Contains(got, "uuid-square") {
		t.Errorf("ImageURL = %q, want it to contain uuid-square", got)
	}
}

func TestImageURL_FallsBackToFirstVisual(t *testing.T) {
	visuals := []Visual{{Name: "some_other", VisualUUID: "uuid-first"}}
	got := ImageURL(visuals, "")
	if !strings.Contains(got, "uuid-first") {
		t.Errorf("ImageURL = %q, want it to contain uuid-first", got)
	}
}
