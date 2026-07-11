package feed

import (
	"context"
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/Aerion/rss-radio-france-pour-tous/internal/radiofrance"
)

var testBuilder = Builder{PublicBaseURL: "https://radio-france-rss.example.com"}

func TestSanitizeXMLText(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"plain text unchanged":        {"hello world", "hello world"},
		"unicode preserved":           {"café LGBT+", "café LGBT+"},
		"tab, newline, CR preserved":  {"a\tb\nc\rd", "a\tb\nc\rd"},
		"control characters stripped": {"a\x0Bb\x00c\x7Fd", "abcd"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := sanitizeXMLText(tc.in); got != tc.want {
				t.Errorf("sanitizeXMLText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func diffusionWithManifestation(id string, createdTime int64) radiofrance.Diffusion {
	d := radiofrance.Diffusion{
		ID:          id,
		Title:       "Episode " + id,
		Path:        "https://www.radiofrance.fr/episode/" + id,
		Standfirst:  "A standfirst",
		CreatedTime: createdTime,
	}
	d.Relationships.Manifestations = []string{"manifestation-" + id}
	return d
}

func testShow() radiofrance.Show {
	return radiofrance.Show{
		ID:         "show-id",
		Title:      "Affaires sensibles",
		Path:       "https://www.radiofrance.fr/franceinter/podcasts/affaires-sensibles",
		Standfirst: "Les grandes affaires des cinquante dernières années.",
	}
}

// sd bundles diffusions and show into a radiofrance.ShowDiffusions, as
// Build now expects.
func sd(diffusions []radiofrance.Diffusion, show radiofrance.Show) radiofrance.ShowDiffusions {
	return radiofrance.ShowDiffusions{Diffusions: diffusions, ShowDetails: show}
}

func TestBuild_ProducesWellFormedXML(t *testing.T) {
	diffusions := []radiofrance.Diffusion{diffusionWithManifestation("d1", 1700000000)}
	out, err := testBuilder.Build(context.Background(), sd(diffusions, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if !strings.Contains(out, `<?xml version="1.0" encoding="UTF-8"?>`) {
		t.Error("missing XML declaration")
	}
	if !strings.Contains(out, "<rss ") || !strings.Contains(out, "</rss>") {
		t.Error("missing rss root element")
	}

	var parsed struct {
		XMLName xml.Name `xml:"rss"`
	}
	if err := xml.Unmarshal([]byte(out), &parsed); err != nil {
		t.Errorf("produced XML does not parse: %v", err)
	}
}

func TestBuild_IncludesShowTitle(t *testing.T) {
	out, err := testBuilder.Build(context.Background(), sd(nil, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(out, "<title>Affaires sensibles</title>") {
		t.Errorf("output does not contain show title:\n%s", out)
	}
}

func TestBuild_OneItemPerDiffusionWithManifestation(t *testing.T) {
	diffusions := []radiofrance.Diffusion{
		diffusionWithManifestation("d1", 1700000000),
		diffusionWithManifestation("d2", 1700001000),
		diffusionWithManifestation("d3", 1700002000),
	}
	out, err := testBuilder.Build(context.Background(), sd(diffusions, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := strings.Count(out, "<item>"); got != 3 {
		t.Errorf("item count = %d, want 3\n%s", got, out)
	}
}

func TestBuild_SkipsDiffusionsWithoutManifestation(t *testing.T) {
	withManifestation := diffusionWithManifestation("d1", 1700000000)
	withoutManifestation := radiofrance.Diffusion{ID: "d2", Title: "No audio", CreatedTime: 1700001000}

	out, err := testBuilder.Build(context.Background(), sd([]radiofrance.Diffusion{withManifestation, withoutManifestation}, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := strings.Count(out, "<item>"); got != 1 {
		t.Errorf("item count = %d, want 1 (diffusion without a manifestation should be skipped)\n%s", got, out)
	}
}

func TestBuild_EnclosureHasAudioMpegType(t *testing.T) {
	diffusions := []radiofrance.Diffusion{diffusionWithManifestation("d1", 1700000000)}
	out, err := testBuilder.Build(context.Background(), sd(diffusions, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(out, `type="audio/mpeg"`) {
		t.Errorf("output does not contain an audio/mpeg enclosure:\n%s", out)
	}
}

func TestBuild_UnicodeTitleSurvives(t *testing.T) {
	d := diffusionWithManifestation("d1", 1700000000)
	d.Title = "25 juin 1977, la première Marche des fiertés LGBT+"
	out, err := testBuilder.Build(context.Background(), sd([]radiofrance.Diffusion{d}, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(out, "25 juin 1977") || !strings.Contains(out, "première") || !strings.Contains(out, "LGBT+") {
		t.Errorf("unicode title was mangled:\n%s", out)
	}
}

func TestBuild_NextPageLink(t *testing.T) {
	diffusions := []radiofrance.Diffusion{diffusionWithManifestation("d1", 1700000000)}

	withNext, err := testBuilder.Build(context.Background(), sd(diffusions, testShow()), "https://radio-france-rss.example.com/rss/show-id?page=1")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(withNext, `rel="next"`) || !strings.Contains(withNext, "page=1") {
		t.Errorf("expected atom:link rel=next with the next page URL:\n%s", withNext)
	}

	withoutNext, err := testBuilder.Build(context.Background(), sd(diffusions, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if strings.Contains(withoutNext, `rel="next"`) {
		t.Errorf("did not expect atom:link rel=next when nextPageURL is empty:\n%s", withoutNext)
	}
}

func TestBuild_EscapesSpecialCharacters(t *testing.T) {
	show := testShow()
	show.Title = `Le Podcast <Super> & "Cool"`
	out, err := testBuilder.Build(context.Background(), sd(nil, show), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if strings.Contains(out, "<Super>") {
		t.Errorf("title was not escaped:\n%s", out)
	}
	if !strings.Contains(out, "&lt;Super&gt;") || !strings.Contains(out, "&amp;") {
		t.Errorf("expected escaped entities in output:\n%s", out)
	}
}

func TestBuild_StripsInvalidXMLCharacters(t *testing.T) {
	d := diffusionWithManifestation("d1", 1700000000)
	d.Title = "Bad\x0Bchars\x00here"
	out, err := testBuilder.Build(context.Background(), sd([]radiofrance.Diffusion{d}, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if strings.ContainsRune(out, 0x0B) || strings.ContainsRune(out, 0x00) {
		t.Errorf("invalid XML characters leaked into output:\n%s", out)
	}
	if !strings.Contains(out, "Badcharshere") {
		t.Errorf("expected sanitized title to remain readable:\n%s", out)
	}
}

func TestGUID_BackwardCompatibilityCutoff(t *testing.T) {
	cases := []struct {
		name        string
		createdTime int64 // unix seconds
		wantGUIDIs  string
	}{
		{"well before cutoff", guidCutoff.Add(-24 * time.Hour).Unix(), "manifestation-id"},
		{"exactly at cutoff", guidCutoff.Unix(), "manifestation-id"},
		{"one second after cutoff", guidCutoff.Add(1 * time.Second).Unix(), "diffusion-id"},
		{"well after cutoff", guidCutoff.Add(24 * time.Hour).Unix(), "diffusion-id"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := radiofrance.Diffusion{ID: "diffusion-id", CreatedTime: tc.createdTime}
			d.Relationships.Manifestations = []string{"manifestation-id"}

			got := guid(d, "manifestation-id")
			if got != tc.wantGUIDIs {
				t.Errorf("guid() = %q, want %q", got, tc.wantGUIDIs)
			}
		})
	}
}

// fakeResolver is a stand-in for internal/episodecache.Resolver, letting
// these tests exercise duration rendering without a real cache/API.
type fakeResolver struct {
	durationsByDiffusionID map[string]time.Duration
}

func (f fakeResolver) Resolve(ctx context.Context, showID, showTitle string, d radiofrance.Diffusion, included map[string]radiofrance.ManifestationDetails) (string, time.Duration) {
	return d.ManifestationID(), f.durationsByDiffusionID[d.ID]
}

func TestBuild_IncludesDurationWhenResolverProvidesIt(t *testing.T) {
	b := Builder{
		PublicBaseURL: testBuilder.PublicBaseURL,
		Resolver:      fakeResolver{durationsByDiffusionID: map[string]time.Duration{"d1": 90*time.Minute + 5*time.Second}},
	}
	diffusions := []radiofrance.Diffusion{diffusionWithManifestation("d1", 1700000000)}

	out, err := b.Build(context.Background(), sd(diffusions, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(out, "<itunes:duration>01:30:05</itunes:duration>") {
		t.Errorf("expected formatted duration in output:\n%s", out)
	}
}

func TestBuild_OmitsDurationWhenUnresolved(t *testing.T) {
	diffusions := []radiofrance.Diffusion{diffusionWithManifestation("d1", 1700000000)}
	// testBuilder has no Resolver configured.
	out, err := testBuilder.Build(context.Background(), sd(diffusions, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if strings.Contains(out, "itunes:duration") {
		t.Errorf("did not expect an itunes:duration element:\n%s", out)
	}
}

func TestBuild_IncludesItunesCategoryWhenPresent(t *testing.T) {
	show := testShow()
	show.Extra.ItunesCat = "Society & Culture"
	show.Extra.ItunesSubCat = "Documentary"

	out, err := testBuilder.Build(context.Background(), sd(nil, show), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(out, `<itunes:category text="Society &amp; Culture">`) {
		t.Errorf("expected top-level itunes:category:\n%s", out)
	}
	if !strings.Contains(out, `<itunes:category text="Documentary">`) {
		t.Errorf("expected nested subcategory:\n%s", out)
	}
}

func TestBuild_OmitsItunesCategoryWhenAbsent(t *testing.T) {
	out, err := testBuilder.Build(context.Background(), sd(nil, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if strings.Contains(out, "itunes:category") {
		t.Errorf("did not expect an itunes:category element:\n%s", out)
	}
}
