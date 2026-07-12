// Package feed builds the RSS/podcast XML feed served at /rss/{showId}.
package feed

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/yuin/goldmark"
	"golang.org/x/sync/errgroup"

	"github.com/Aerion/rss-radio-france-pour-tous/internal/radiofrance"
)

// guidCutoff is the boundary used to keep RSS guids stable for existing
// subscribers: diffusions created on or before this date keep using their
// manifestation ID as the guid (the original, pre-2022 scheme), while newer
// ones use the diffusion ID. Changing this would reset read/download state
// for every existing subscriber, so it must never move.
var guidCutoff = time.Date(2022, 9, 12, 0, 0, 0, 0, time.UTC)

// resolveConcurrency bounds how many manifestations are resolved (cache
// lookup, or API fetch on a cache miss) at once while building a feed page
// - without this, a cold cache on a ~100-episode page would fire 100
// concurrent Radio France requests.
const resolveConcurrency = 8

// ManifestationResolver looks up the manifestation ID, playable URL, and
// duration to use for a diffusion's enclosure/itunes:duration - typically
// backed by internal/episodecache, consulting a cache (and preferring the
// API's Principal manifestation) before falling back to the Radio France
// API. included is whatever manifestation data came back inline with the
// diffusions page (see radiofrance.ShowDiffusions.Manifestations). url is
// "" if no playable URL could be resolved, in which case Build falls back
// to the legacy /audio/ redirect for that item.
// Declared here rather than importing episodecache directly so this
// package doesn't need to know about caching/storage at all.
type ManifestationResolver interface {
	Resolve(ctx context.Context, showID, showTitle string, d radiofrance.Diffusion, included map[string]radiofrance.ManifestationDetails) (manifestationID, url string, duration time.Duration)
}

// ImageResolver picks the cover image URL to use for a diffusion, typically
// backed by internal/episodecache. Unlike the pure radiofrance.DiffusionImageURL,
// it can follow a rerun's origin diffusion (an upstream call, cached) to find
// the real per-episode artwork when the rerun itself carries none - see
// episodecache.Resolver.ResolveImage. Declared here rather than importing
// episodecache directly for the same reason as ManifestationResolver.
type ImageResolver interface {
	ResolveImage(ctx context.Context, d radiofrance.Diffusion) string
}

// DescriptionResolver picks the (bodyMarkdown, standfirst) pair to use for a
// diffusion's feed description, typically backed by internal/episodecache.
// Unlike using d.BodyMarkdown/d.Standfirst directly, it can follow a rerun's
// origin diffusion (an upstream call, cached) to find the original
// broadcast's full editorial notes when the rerun's own copy is a flattened,
// auto-derived one - see episodecache.Resolver.ResolveDescription. Declared
// here rather than importing episodecache directly for the same reason as
// ManifestationResolver.
type DescriptionResolver interface {
	ResolveDescription(ctx context.Context, d radiofrance.Diffusion) (bodyMarkdown, standfirst string, originCreatedTime int64)
}

// Builder builds RSS feeds for shows.
type Builder struct {
	// PublicBaseURL is this app's own externally-visible base URL, used to
	// build the legacy /audio/ redirect URL for an item whose playable URL
	// couldn't be resolved directly (see buildItem). New enclosures point
	// straight at the resolved manifestation URL instead.
	PublicBaseURL string

	// Resolver is nil-able. When nil, Build falls back to using each
	// diffusion's raw ManifestationID via the /audio/ redirect, with no
	// duration - the pre-Phase-4 behavior - which keeps existing tests
	// simple and gives every caller an escape hatch if caching is
	// unavailable.
	Resolver ManifestationResolver

	// ImageResolver is nil-able. When nil, Build falls back to
	// radiofrance.DiffusionImageURL(d) directly, with no rerun/origin
	// handling - same escape hatch rationale as Resolver.
	ImageResolver ImageResolver

	// DescriptionResolver is nil-able. When nil, Build falls back to using
	// d.BodyMarkdown/d.Standfirst directly, with no rerun/origin handling -
	// same escape hatch rationale as Resolver.
	DescriptionResolver DescriptionResolver
}

// Build renders an RSS 2.0 feed for one page of a show's diffusions.
// nextPageURL, if non-empty, is advertised as an atom:link rel="next" for
// feed readers that support paginated feeds (RFC 5005).
func (b Builder) Build(ctx context.Context, sd radiofrance.ShowDiffusions, nextPageURL string) (string, error) {
	show := sd.ShowDetails
	resolved := b.resolveAll(ctx, sd)

	items := make([]item, 0, len(sd.Diffusions))
	for _, d := range sd.Diffusions {
		it, ok := b.buildItem(d, resolved[d.ID])
		if !ok {
			continue
		}
		items = append(items, it)
	}

	ch := channel{
		Title:       sanitizeXMLText(show.Title),
		Link:        sanitizeXMLText(show.Path),
		Description: sanitizeXMLText(show.Standfirst),
		Items:       items,
	}
	if imgURL := radiofrance.ImageURL(show.Visuals, show.MainImage); imgURL != "" {
		ch.Image = &itunesImage{Href: imgURL}
	}
	if nextPageURL != "" {
		ch.NextLink = &atomLink{Href: nextPageURL, Rel: "next"}
	}
	if show.Extra.ItunesCat != "" {
		cat := &itunesCategory{Text: show.Extra.ItunesCat}
		if show.Extra.ItunesSubCat != "" {
			cat.Subcategory = &itunesCategory{Text: show.Extra.ItunesSubCat}
		}
		ch.Category = cat
	}

	doc := rss{
		Version:         "2.0",
		XMLNSItunes:     "http://www.itunes.com/dtds/podcast-1.0.dtd",
		XMLNSPa:         "http://podcastaddict.com",
		XMLNSPodcastRF:  "http://radiofrance.fr/Lancelot/Podcast#",
		XMLNSGoogleplay: "http://www.google.com/schemas/play-podcasts/1.0",
		XMLNSAtom:       "http://www.w3.org/2005/Atom",
		Channel:         ch,
	}

	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling RSS feed: %w", err)
	}
	return xml.Header + string(body), nil
}

// resolution is what resolveAll produces per diffusion.
type resolution struct {
	manifestationID string
	url             string
	duration        time.Duration
	imageURL        string
	bodyMarkdown    string
	standfirst      string
	// originCreatedTime is the origin diffusion's own CreatedTime (its
	// original broadcast date) as a Unix timestamp when d is a rerun whose
	// origin could be resolved, or 0 otherwise - see
	// DescriptionResolver.ResolveDescription. Used by buildItem to flag a
	// rerun in its description.
	originCreatedTime int64
}

// resolveAll resolves every diffusion's manifestation ID/URL/duration, cover
// image, and description concurrently (bounded by resolveConcurrency), keyed
// by diffusion ID. With none of Resolver/ImageResolver/DescriptionResolver
// configured, the corresponding fields fall back to each diffusion's raw
// ManifestationID/DiffusionImageURL/BodyMarkdown+Standfirst with no upstream
// calls.
func (b Builder) resolveAll(ctx context.Context, sd radiofrance.ShowDiffusions) map[string]resolution {
	results := make(map[string]resolution, len(sd.Diffusions))

	if b.Resolver == nil && b.ImageResolver == nil && b.DescriptionResolver == nil {
		for _, d := range sd.Diffusions {
			results[d.ID] = resolution{
				manifestationID: d.ManifestationID(),
				imageURL:        radiofrance.DiffusionImageURL(d),
				bodyMarkdown:    d.BodyMarkdown,
				standfirst:      d.Standfirst,
			}
		}
		return results
	}

	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(resolveConcurrency)
	for _, d := range sd.Diffusions {
		g.Go(func() error {
			var res resolution
			if b.Resolver != nil {
				res.manifestationID, res.url, res.duration = b.Resolver.Resolve(gctx, sd.ShowDetails.ID, sd.ShowDetails.Title, d, sd.Manifestations)
			} else {
				res.manifestationID = d.ManifestationID()
			}
			if b.ImageResolver != nil {
				res.imageURL = b.ImageResolver.ResolveImage(gctx, d)
			} else {
				res.imageURL = radiofrance.DiffusionImageURL(d)
			}
			if b.DescriptionResolver != nil {
				res.bodyMarkdown, res.standfirst, res.originCreatedTime = b.DescriptionResolver.ResolveDescription(gctx, d)
			} else {
				res.bodyMarkdown, res.standfirst = d.BodyMarkdown, d.Standfirst
			}
			mu.Lock()
			results[d.ID] = res
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait() // Resolve/ResolveImage/ResolveDescription degrade gracefully on error rather than failing; nothing to propagate here.
	return results
}

func (b Builder) buildItem(d radiofrance.Diffusion, res resolution) (item, bool) {
	manifestationID := res.manifestationID
	if manifestationID == "" {
		slog.Info("diffusion has no audio manifestation, skipping", "diffusionID", d.ID, "path", d.Path)
		return item{}, false
	}

	description := stripShortcodes(res.bodyMarkdown)
	if isPlaceholder(description) {
		description = res.standfirst
		if isPlaceholder(description) {
			description = ""
		}
	} else {
		if res.originCreatedTime > 0 {
			description = rerunBanner(res.originCreatedTime) + description
		}
		description = renderMarkdown(description)
	}

	audioURL := res.url
	if audioURL == "" {
		// The resolver couldn't produce a direct playable URL (no Resolver
		// configured, or every candidate manifestation failed to resolve) -
		// fall back to the legacy /audio/ redirect, which gets its own shot
		// at resolving the URL (cache or live fetch) when a listener
		// actually plays the episode.
		audioURL = fmt.Sprintf("%s/audio/%s", b.PublicBaseURL, manifestationID)
	}

	it := item{
		Title:       sanitizeXMLText(d.Title),
		GUID:        guid(d, manifestationID),
		Link:        sanitizeXMLText(d.Path),
		Description: sanitizeXMLText(description),
		Enclosure: enclosure{
			URL:  audioURL,
			Type: "audio/mpeg",
		},
		PubDate: time.Unix(d.CreatedTime, 0).UTC().Format(http.TimeFormat),
	}
	if res.duration > 0 {
		it.Duration = formatItunesDuration(res.duration)
	}
	if res.imageURL != "" {
		it.Image = &itunesImage{Href: res.imageURL}
	}
	return it, true
}

// isPlaceholder reports whether s is empty or contains nothing but
// whitespace/periods. Some diffusions carry a placeholder like "." or " "
// in bodyMarkdown or standfirst instead of a genuinely empty string when
// there's no real text - live samples found this on roughly half of one
// show's episodes and ~10-30% of another's, so checking for "" alone
// misses most of them. buildItem prefers the full bodyMarkdown (long-form
// show notes) as the description, falling back to standfirst only when
// bodyMarkdown is itself a placeholder.
func isPlaceholder(s string) bool {
	return strings.TrimFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || r == '.'
	}) == ""
}

// rerunBanner returns a small italic markdown line flagging a rerun
// episode's original broadcast date, meant to be prepended to its
// bodyMarkdown-derived description before rendering. Only applied on that
// branch (not the standfirst fallback, which is used verbatim - see
// isPlaceholder's caller in buildItem), so a rerun whose bodyMarkdown is
// itself a placeholder won't carry this banner.
func rerunBanner(originCreatedTime int64) string {
	date := time.Unix(originCreatedTime, 0).UTC().Format("2006-01-02")
	return fmt.Sprintf("*Rediffusion de l'épisode du %s*\n\n", date)
}

var (
	shortcodePattern    = regexp.MustCompile(`\{%.*?%\}`)
	multiSpacePattern   = regexp.MustCompile(`[ \t]{2,}`)
	multiNewlinePattern = regexp.MustCompile(`\n{3,}`)
)

// stripShortcodes removes Radio France's CMS templating shortcodes from
// bodyMarkdown text before it's used as a feed description fallback - e.g.
// "{% bounce 1 <uuid> <url-encoded title> %}", a cross-promotion call-out
// their own app/website renders as a card. Left in raw, a shortcode like
// that shows up as ugly URL-encoded gibberish in a plain-text RSS
// description instead of being invisible the way it is in their own app.
// Also collapses whatever extra whitespace/blank lines a removed shortcode
// leaves behind.
func stripShortcodes(s string) string {
	s = shortcodePattern.ReplaceAllString(s, "")
	s = multiSpacePattern.ReplaceAllString(s, " ")
	s = multiNewlinePattern.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// renderMarkdown converts bodyMarkdown to HTML so podcast apps render its
// formatting (bold, links, lists, ...) instead of showing raw markdown
// syntax. The resulting tags go into item.Description as ordinary text, so
// encoding/xml escapes them into entities on Marshal (e.g. "&lt;p&gt;") -
// the standard way feed readers carry HTML inside <description>, which they
// unescape and render themselves. On a render error, s is used unchanged so
// a malformed body degrades to plain text rather than dropping the episode.
func renderMarkdown(s string) string {
	var buf bytes.Buffer
	if err := goldmark.Convert([]byte(s), &buf); err != nil {
		return s
	}
	return strings.TrimSpace(buf.String())
}

// formatItunesDuration renders d as HH:MM:SS, the conventional
// itunes:duration format.
func formatItunesDuration(d time.Duration) string {
	total := int(d.Seconds())
	hours := total / 3600
	minutes := (total % 3600) / 60
	seconds := total % 60
	return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
}

// guid returns the RSS guid for a diffusion, preserving the pre-2022
// numbering scheme for old episodes so existing subscribers' read state
// doesn't reset. See guidCutoff.
func guid(d radiofrance.Diffusion, manifestationID string) string {
	created := time.Unix(d.CreatedTime, 0).UTC()
	if !created.After(guidCutoff) {
		return manifestationID
	}
	return d.ID
}

// sanitizeXMLText drops characters that are invalid in XML 1.0 text before
// handing a string to encoding/xml, which escapes the five predefined
// entities on Marshal but does not validate or strip characters outside
// the legal XML character set (raw control bytes occasionally present in
// upstream data would otherwise pass straight through). unicode.IsGraphic
// covers this well enough: it excludes controls, surrogates, and
// unassigned/private-use code points, which is what we actually need to
// worry about here.
func sanitizeXMLText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' || unicode.IsGraphic(r) {
			return r
		}
		return -1
	}, s)
}
