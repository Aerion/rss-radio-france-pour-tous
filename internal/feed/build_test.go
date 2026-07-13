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
	out, _, _, err := testBuilder.Build(context.Background(), sd(diffusions, testShow()), "")
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
	out, _, _, err := testBuilder.Build(context.Background(), sd(nil, testShow()), "")
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
	out, _, _, err := testBuilder.Build(context.Background(), sd(diffusions, testShow()), "")
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

	out, _, _, err := testBuilder.Build(context.Background(), sd([]radiofrance.Diffusion{withManifestation, withoutManifestation}, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := strings.Count(out, "<item>"); got != 1 {
		t.Errorf("item count = %d, want 1 (diffusion without a manifestation should be skipped)\n%s", got, out)
	}
}

func TestBuild_EnclosureHasAudioMpegType(t *testing.T) {
	diffusions := []radiofrance.Diffusion{diffusionWithManifestation("d1", 1700000000)}
	out, _, _, err := testBuilder.Build(context.Background(), sd(diffusions, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(out, `type="audio/mpeg"`) {
		t.Errorf("output does not contain an audio/mpeg enclosure:\n%s", out)
	}
}

func TestBuild_DescriptionFallback(t *testing.T) {
	cases := map[string]struct {
		standfirst   string
		bodyMarkdown string
		want         string
	}{
		"real bodyMarkdown rendered as HTML, standfirst ignored": {
			standfirst:   "A real teaser",
			bodyMarkdown: "Long form notes follow here.",
			want:         "<p>Long form notes follow here.</p>",
		},
		"placeholder bodyMarkdown (period) falls back to standfirst": {
			standfirst:   "A real teaser",
			bodyMarkdown: ".",
			want:         "A real teaser",
		},
		"placeholder bodyMarkdown (whitespace) falls back to standfirst": {
			standfirst:   "A real teaser",
			bodyMarkdown: "  ",
			want:         "A real teaser",
		},
		"empty bodyMarkdown falls back to standfirst": {
			standfirst:   "A real teaser",
			bodyMarkdown: "",
			want:         "A real teaser",
		},
		"both placeholder yields empty description": {
			standfirst: ".", bodyMarkdown: " . ",
			want: "",
		},
		"both empty yields empty description": {
			standfirst: "", bodyMarkdown: "",
			want: "",
		},
		"bodyMarkdown strips shortcodes and renders as HTML": {
			standfirst:   "ignored",
			bodyMarkdown: "Des espions et des livres.\n\n{% bounce 1 abc123 %22title%22 %}\n\nLa suite.",
			want:         "<p>Des espions et des livres.</p>\n<p>La suite.</p>",
		},
		"standfirst fallback used verbatim, shortcodes not stripped": {
			standfirst:   "A teaser with {% something %} inside",
			bodyMarkdown: ".",
			want:         "A teaser with {% something %} inside",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			d := diffusionWithManifestation("d1", 1700000000)
			d.Standfirst = tc.standfirst
			d.BodyMarkdown = tc.bodyMarkdown

			out, _, _, err := testBuilder.Build(context.Background(), sd([]radiofrance.Diffusion{d}, testShow()), "")
			if err != nil {
				t.Fatalf("Build: %v", err)
			}

			// Parse rather than substring-match: encoding/xml escapes
			// newlines within text as "&#xA;" (valid, semantically
			// identical to a raw newline), so comparing decoded content
			// is the correct check here, not a literal string match.
			var parsed struct {
				Item struct {
					Description string `xml:"description"`
				} `xml:"channel>item"`
			}
			if err := xml.Unmarshal([]byte(out), &parsed); err != nil {
				t.Fatalf("output does not parse as XML: %v\n%s", err, out)
			}
			if parsed.Item.Description != tc.want {
				t.Errorf("description = %q, want %q", parsed.Item.Description, tc.want)
			}
		})
	}
}

func TestBuild_UnicodeTitleSurvives(t *testing.T) {
	d := diffusionWithManifestation("d1", 1700000000)
	d.Title = "25 juin 1977, la première Marche des fiertés LGBT+"
	out, _, _, err := testBuilder.Build(context.Background(), sd([]radiofrance.Diffusion{d}, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(out, "25 juin 1977") || !strings.Contains(out, "première") || !strings.Contains(out, "LGBT+") {
		t.Errorf("unicode title was mangled:\n%s", out)
	}
}

func TestBuild_NextPageLink(t *testing.T) {
	diffusions := []radiofrance.Diffusion{diffusionWithManifestation("d1", 1700000000)}

	withNext, _, _, err := testBuilder.Build(context.Background(), sd(diffusions, testShow()), "https://radio-france-rss.example.com/rss/show-id?page=1")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(withNext, `rel="next"`) || !strings.Contains(withNext, "page=1") {
		t.Errorf("expected atom:link rel=next with the next page URL:\n%s", withNext)
	}

	withoutNext, _, _, err := testBuilder.Build(context.Background(), sd(diffusions, testShow()), "")
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
	out, _, _, err := testBuilder.Build(context.Background(), sd(nil, show), "")
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
	out, _, _, err := testBuilder.Build(context.Background(), sd([]radiofrance.Diffusion{d}, testShow()), "")
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

func TestIsPlaceholder(t *testing.T) {
	cases := map[string]bool{
		"":           true,
		".":          true,
		" ":          true,
		"  .  ":      true,
		"...":        true,
		"Real text":  false,
		"Real text.": false,
		".Real text": false,
		"a":          false,
	}
	for in, want := range cases {
		if got := isPlaceholder(in); got != want {
			t.Errorf("isPlaceholder(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestStripShortcodes(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"no shortcode, unchanged": {
			"Just plain text.", "Just plain text.",
		},
		"real example: bounce shortcode removed": {
			`Des espions et des livres.

{% bounce 1 66246ef9-0de7-464e-96e9-2d3ec6f49c49 %22L%E2%80%99%C3%A9tendard%20sanglant%20est%20lev%C3%A9%22%20de%20Benjamin%20Dierstein%C2%A0%3A%20un%20roman%20noir%20dans%20la%20France%201980-1982 %C3%80%20%C3%A9couter %}

La suite de l'histoire.`,
			"Des espions et des livres.\n\nLa suite de l'histoire.",
		},
		"shortcode inline within a sentence": {
			"Before {% image abc123 %} after.", "Before after.",
		},
		"multiple shortcodes": {
			"A {% foo 1 %} B {% bar 2 %} C", "A B C",
		},
		"only a shortcode yields empty": {
			"{% bounce 1 abc %}", "",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := stripShortcodes(tc.in); got != tc.want {
				t.Errorf("stripShortcodes(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
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
// these tests exercise duration/URL rendering without a real cache/API.
type fakeResolver struct {
	durationsByDiffusionID map[string]time.Duration
	urlsByDiffusionID      map[string]string
	expiresAtByDiffusionID map[string]*time.Time
}

func (f fakeResolver) Resolve(ctx context.Context, showID, showTitle string, d radiofrance.Diffusion, included map[string]radiofrance.ManifestationDetails) (string, string, time.Duration, *time.Time) {
	return d.ManifestationID(), f.urlsByDiffusionID[d.ID], f.durationsByDiffusionID[d.ID], f.expiresAtByDiffusionID[d.ID]
}

// fakeImageResolver is a stand-in for internal/episodecache.Resolver's
// ResolveImage method.
type fakeImageResolver struct {
	imageURLsByDiffusionID map[string]string
}

func (f fakeImageResolver) ResolveImage(ctx context.Context, d radiofrance.Diffusion) string {
	return f.imageURLsByDiffusionID[d.ID]
}

func TestBuild_UsesImageResolverWhenConfigured(t *testing.T) {
	b := Builder{
		PublicBaseURL: testBuilder.PublicBaseURL,
		ImageResolver: fakeImageResolver{imageURLsByDiffusionID: map[string]string{
			"d1": "https://api.radiofrance.fr/v1/services/embed/image/uuid-origin?preset=568x568",
		}},
	}
	// The diffusion's own visuals point elsewhere - the resolver's answer
	// must win, proving Build defers to it rather than DiffusionImageURL.
	d := diffusionWithManifestation("d1", 1700000000)
	d.Visuals = []radiofrance.Visual{{Name: "square_banner", VisualUUID: "uuid-show-banner"}}

	out, _, _, err := b.Build(context.Background(), sd([]radiofrance.Diffusion{d}, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(out, "uuid-origin") {
		t.Errorf("expected the resolver's image URL in output:\n%s", out)
	}
	if strings.Contains(out, "uuid-show-banner") {
		t.Errorf("did not expect the raw visuals-based image URL in output:\n%s", out)
	}
}

func TestBuild_FallsBackToDiffusionImageURLWhenNoImageResolver(t *testing.T) {
	d := diffusionWithManifestation("d1", 1700000000)
	d.MainImage = "uuid-episode"

	// testBuilder has no ImageResolver configured.
	out, _, _, err := testBuilder.Build(context.Background(), sd([]radiofrance.Diffusion{d}, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(out, "uuid-episode") {
		t.Errorf("expected DiffusionImageURL's MainImage-based URL in output:\n%s", out)
	}
}

// fakeDescriptionResolver is a stand-in for internal/episodecache.Resolver's
// ResolveDescription method.
type fakeDescriptionResolver struct {
	bodyMarkdownByDiffusionID      map[string]string
	standfirstByDiffusionID        map[string]string
	originCreatedTimeByDiffusionID map[string]int64
}

func (f fakeDescriptionResolver) ResolveDescription(ctx context.Context, d radiofrance.Diffusion) (string, string, int64) {
	return f.bodyMarkdownByDiffusionID[d.ID], f.standfirstByDiffusionID[d.ID], f.originCreatedTimeByDiffusionID[d.ID]
}

func TestBuild_UsesDescriptionResolverWhenConfigured(t *testing.T) {
	b := Builder{
		PublicBaseURL: testBuilder.PublicBaseURL,
		DescriptionResolver: fakeDescriptionResolver{bodyMarkdownByDiffusionID: map[string]string{
			"d1": "**rich** origin body",
		}},
	}
	// The diffusion's own bodyMarkdown says something else - the resolver's
	// answer must win, proving Build defers to it rather than d.BodyMarkdown.
	d := diffusionWithManifestation("d1", 1700000000)
	d.BodyMarkdown = "flattened rerun body"

	out, _, _, err := b.Build(context.Background(), sd([]radiofrance.Diffusion{d}, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(out, "rich") {
		t.Errorf("expected the resolver's body markdown in output:\n%s", out)
	}
	if strings.Contains(out, "flattened rerun body") {
		t.Errorf("did not expect the diffusion's own bodyMarkdown in output:\n%s", out)
	}
}

func TestBuild_FallsBackToOwnFieldsWhenNoDescriptionResolver(t *testing.T) {
	d := diffusionWithManifestation("d1", 1700000000)
	d.BodyMarkdown = "own body"

	// testBuilder has no DescriptionResolver configured.
	out, _, _, err := testBuilder.Build(context.Background(), sd([]radiofrance.Diffusion{d}, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(out, "own body") {
		t.Errorf("expected the diffusion's own bodyMarkdown in output:\n%s", out)
	}
}

func TestBuild_RerunDescriptionIncludesOriginBroadcastDateBanner(t *testing.T) {
	b := Builder{
		PublicBaseURL: testBuilder.PublicBaseURL,
		DescriptionResolver: fakeDescriptionResolver{
			bodyMarkdownByDiffusionID:      map[string]string{"d1": "The real notes."},
			originCreatedTimeByDiffusionID: map[string]int64{"d1": 1704067200}, // 2024-01-01
		},
	}
	d := diffusionWithManifestation("d1", 1700000000)

	out, _, _, err := b.Build(context.Background(), sd([]radiofrance.Diffusion{d}, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// encoding/xml escapes the rendered HTML into entities within
	// <description> (see renderMarkdown's doc comment), so match the
	// escaped form rather than raw "<em>".
	if !strings.Contains(out, "&lt;em&gt;Rediffusion de l") || !strings.Contains(out, "2024-01-01") {
		t.Errorf("expected an italic rerun banner with the origin broadcast date in output:\n%s", out)
	}
	if !strings.Contains(out, "The real notes.") {
		t.Errorf("expected the resolved bodyMarkdown alongside the banner in output:\n%s", out)
	}
}

func TestBuild_NoRerunBannerWhenOriginCreatedTimeZero(t *testing.T) {
	b := Builder{
		PublicBaseURL: testBuilder.PublicBaseURL,
		DescriptionResolver: fakeDescriptionResolver{
			bodyMarkdownByDiffusionID: map[string]string{"d1": "The real notes."},
		},
	}
	d := diffusionWithManifestation("d1", 1700000000)

	out, _, _, err := b.Build(context.Background(), sd([]radiofrance.Diffusion{d}, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if strings.Contains(out, "Rediffusion") {
		t.Errorf("did not expect a rerun banner when originCreatedTime is 0:\n%s", out)
	}
}

// fakeTitleResolver is a stand-in for internal/episodecache.Resolver's
// ResolveTitle method.
type fakeTitleResolver struct {
	titleByDiffusionID map[string]string
}

func (f fakeTitleResolver) ResolveTitle(ctx context.Context, d radiofrance.Diffusion) string {
	return f.titleByDiffusionID[d.ID]
}

func TestBuild_UsesTitleResolverWhenConfigured(t *testing.T) {
	b := Builder{
		PublicBaseURL: testBuilder.PublicBaseURL,
		TitleResolver: fakeTitleResolver{titleByDiffusionID: map[string]string{
			"d1": "Real episode title",
		}},
	}
	// The diffusion's own Title says something else - the resolver's
	// answer must win, proving Build defers to it rather than d.Title.
	d := diffusionWithManifestation("d1", 1700000000)
	d.Title = "Generic rerun slot title"

	out, _, _, err := b.Build(context.Background(), sd([]radiofrance.Diffusion{d}, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(out, "Real episode title") {
		t.Errorf("expected the resolver's title in output:\n%s", out)
	}
	if strings.Contains(out, "Generic rerun slot title") {
		t.Errorf("did not expect the diffusion's own Title in output:\n%s", out)
	}
}

func TestBuild_FallsBackToOwnTitleWhenNoTitleResolver(t *testing.T) {
	d := diffusionWithManifestation("d1", 1700000000)
	d.Title = "Own title"

	// testBuilder has no TitleResolver configured.
	out, _, _, err := testBuilder.Build(context.Background(), sd([]radiofrance.Diffusion{d}, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(out, "Own title") {
		t.Errorf("expected the diffusion's own Title in output:\n%s", out)
	}
}

func TestBuild_IncludesDurationWhenResolverProvidesIt(t *testing.T) {
	b := Builder{
		PublicBaseURL: testBuilder.PublicBaseURL,
		Resolver:      fakeResolver{durationsByDiffusionID: map[string]time.Duration{"d1": 90*time.Minute + 5*time.Second}},
	}
	diffusions := []radiofrance.Diffusion{diffusionWithManifestation("d1", 1700000000)}

	out, _, _, err := b.Build(context.Background(), sd(diffusions, testShow()), "")
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
	out, _, _, err := testBuilder.Build(context.Background(), sd(diffusions, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if strings.Contains(out, "itunes:duration") {
		t.Errorf("did not expect an itunes:duration element:\n%s", out)
	}
}

func TestBuild_EnclosureUsesResolvedURLDirectly(t *testing.T) {
	b := Builder{
		PublicBaseURL: testBuilder.PublicBaseURL,
		Resolver:      fakeResolver{urlsByDiffusionID: map[string]string{"d1": "https://media.radiofrance-podcast.net/d1.mp3"}},
	}
	diffusions := []radiofrance.Diffusion{diffusionWithManifestation("d1", 1700000000)}

	out, _, _, err := b.Build(context.Background(), sd(diffusions, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(out, `url="https://media.radiofrance-podcast.net/d1.mp3"`) {
		t.Errorf("expected enclosure to use the resolver's URL directly:\n%s", out)
	}
	if strings.Contains(out, "/audio/") {
		t.Errorf("did not expect the legacy /audio/ redirect when the resolver provides a URL:\n%s", out)
	}
}

func TestBuild_EnclosureFallsBackToLegacyAudioRouteWhenURLUnresolved(t *testing.T) {
	diffusions := []radiofrance.Diffusion{diffusionWithManifestation("d1", 1700000000)}
	// testBuilder has no Resolver configured, so no URL is ever resolved.
	out, _, _, err := testBuilder.Build(context.Background(), sd(diffusions, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(out, "https://radio-france-rss.example.com/audio/manifestation-d1") {
		t.Errorf("expected enclosure to fall back to the legacy /audio/ redirect:\n%s", out)
	}
}

func TestBuild_IncludesItunesCategoryWhenPresent(t *testing.T) {
	show := testShow()
	show.Extra.ItunesCat = "Society & Culture"
	show.Extra.ItunesSubCat = "Documentary"

	out, _, _, err := testBuilder.Build(context.Background(), sd(nil, show), "")
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
	out, _, _, err := testBuilder.Build(context.Background(), sd(nil, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if strings.Contains(out, "itunes:category") {
		t.Errorf("did not expect an itunes:category element:\n%s", out)
	}
}

func TestBuild_HadDegradedTrueWhenDurationUnresolved(t *testing.T) {
	diffusions := []radiofrance.Diffusion{diffusionWithManifestation("d1", 1700000000)}
	// testBuilder has no Resolver configured, so duration is never resolved.
	_, hadDegraded, _, err := testBuilder.Build(context.Background(), sd(diffusions, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !hadDegraded {
		t.Error("expected hadDegraded = true when no item's duration is resolved")
	}
}

func TestBuild_HadDegradedFalseWhenFullyResolved(t *testing.T) {
	b := Builder{
		PublicBaseURL: testBuilder.PublicBaseURL,
		Resolver:      fakeResolver{durationsByDiffusionID: map[string]time.Duration{"d1": 90 * time.Second}},
	}
	diffusions := []radiofrance.Diffusion{diffusionWithManifestation("d1", 1700000000)}

	_, hadDegraded, _, err := b.Build(context.Background(), sd(diffusions, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if hadDegraded {
		t.Error("expected hadDegraded = false when every item's duration is resolved and none are reruns")
	}
}

func TestBuild_HadDegradedTrueWhenRerunOriginUnresolved(t *testing.T) {
	b := Builder{
		PublicBaseURL:       testBuilder.PublicBaseURL,
		Resolver:            fakeResolver{durationsByDiffusionID: map[string]time.Duration{"d1": 90 * time.Second}},
		DescriptionResolver: fakeDescriptionResolver{}, // no entry for d1 -> originCreatedTime 0
	}
	d := diffusionWithManifestation("d1", 1700000000)
	d.Relationships.OriginDiffusion = []string{"origin1"}

	_, hadDegraded, _, err := b.Build(context.Background(), sd([]radiofrance.Diffusion{d}, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !hadDegraded {
		t.Error("expected hadDegraded = true when a rerun's origin hasn't resolved yet (originCreatedTime == 0)")
	}
}

func TestBuild_HadDegradedFalseWhenRerunOriginResolved(t *testing.T) {
	b := Builder{
		PublicBaseURL:       testBuilder.PublicBaseURL,
		Resolver:            fakeResolver{durationsByDiffusionID: map[string]time.Duration{"d1": 90 * time.Second}},
		DescriptionResolver: fakeDescriptionResolver{originCreatedTimeByDiffusionID: map[string]int64{"d1": 1704067200}},
	}
	d := diffusionWithManifestation("d1", 1700000000)
	d.Relationships.OriginDiffusion = []string{"origin1"}

	_, hadDegraded, _, err := b.Build(context.Background(), sd([]radiofrance.Diffusion{d}, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if hadDegraded {
		t.Error("expected hadDegraded = false when the rerun's origin has resolved")
	}
}

func TestBuild_EarliestExpiryNilWhenNoneKnown(t *testing.T) {
	diffusions := []radiofrance.Diffusion{diffusionWithManifestation("d1", 1700000000)}
	// testBuilder has no Resolver configured, so no expiration is ever known.
	_, _, earliestExpiry, err := testBuilder.Build(context.Background(), sd(diffusions, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if earliestExpiry != nil {
		t.Errorf("earliestExpiry = %v, want nil", earliestExpiry)
	}
}

func TestBuild_EarliestExpiryIsMinimumAcrossItems(t *testing.T) {
	sooner := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	later := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	b := Builder{
		PublicBaseURL: testBuilder.PublicBaseURL,
		Resolver: fakeResolver{
			durationsByDiffusionID: map[string]time.Duration{"d1": 90 * time.Second, "d2": 90 * time.Second},
			expiresAtByDiffusionID: map[string]*time.Time{"d1": &later, "d2": &sooner},
		},
	}
	diffusions := []radiofrance.Diffusion{
		diffusionWithManifestation("d1", 1700000000),
		diffusionWithManifestation("d2", 1700001000),
	}

	_, _, earliestExpiry, err := b.Build(context.Background(), sd(diffusions, testShow()), "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if earliestExpiry == nil || !earliestExpiry.Equal(sooner) {
		t.Errorf("earliestExpiry = %v, want %v", earliestExpiry, sooner)
	}
}
