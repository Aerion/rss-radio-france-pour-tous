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

// ManifestationResolver looks up the manifestation ID and duration to use
// for a diffusion's enclosure/itunes:duration - typically backed by
// internal/episodecache, consulting a cache (and preferring the API's
// Principal manifestation) before falling back to the Radio France API.
// included is whatever manifestation data came back inline with the
// diffusions page (see radiofrance.ShowDiffusions.Manifestations).
// Declared here rather than importing episodecache directly so this
// package doesn't need to know about caching/storage at all.
type ManifestationResolver interface {
	Resolve(ctx context.Context, showID, showTitle string, d radiofrance.Diffusion, included map[string]radiofrance.ManifestationDetails) (manifestationID string, duration time.Duration)
}

// Builder builds RSS feeds for shows.
type Builder struct {
	// PublicBaseURL is this app's own externally-visible base URL, used to
	// build enclosure URLs that point back at our /audio/ redirect route.
	PublicBaseURL string

	// Resolver is nil-able. When nil, Build falls back to using each
	// diffusion's raw ManifestationID with no duration - the pre-Phase-4
	// behavior - which keeps existing tests simple and gives every caller
	// an escape hatch if caching is unavailable.
	Resolver ManifestationResolver
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
	duration        time.Duration
}

// resolveAll resolves every diffusion's manifestation ID/duration
// concurrently (bounded by resolveConcurrency), keyed by diffusion ID. With
// no Resolver configured, this is just each diffusion's raw
// ManifestationID with no upstream calls at all.
func (b Builder) resolveAll(ctx context.Context, sd radiofrance.ShowDiffusions) map[string]resolution {
	results := make(map[string]resolution, len(sd.Diffusions))

	if b.Resolver == nil {
		for _, d := range sd.Diffusions {
			results[d.ID] = resolution{manifestationID: d.ManifestationID()}
		}
		return results
	}

	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(resolveConcurrency)
	for _, d := range sd.Diffusions {
		g.Go(func() error {
			manifestationID, duration := b.Resolver.Resolve(gctx, sd.ShowDetails.ID, sd.ShowDetails.Title, d, sd.Manifestations)
			mu.Lock()
			results[d.ID] = resolution{manifestationID: manifestationID, duration: duration}
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait() // Resolve degrades gracefully on error rather than failing; nothing to propagate here.
	return results
}

func (b Builder) buildItem(d radiofrance.Diffusion, res resolution) (item, bool) {
	manifestationID := res.manifestationID
	if manifestationID == "" {
		slog.Info("diffusion has no audio manifestation, skipping", "diffusionID", d.ID, "path", d.Path)
		return item{}, false
	}

	description := stripShortcodes(d.BodyMarkdown)
	if isPlaceholder(description) {
		description = d.Standfirst
		if isPlaceholder(description) {
			description = ""
		}
	} else {
		description = renderMarkdown(description)
	}

	it := item{
		Title:       sanitizeXMLText(d.Title),
		GUID:        guid(d, manifestationID),
		Link:        sanitizeXMLText(d.Path),
		Description: sanitizeXMLText(description),
		Enclosure: enclosure{
			URL:  fmt.Sprintf("%s/audio/%s", b.PublicBaseURL, manifestationID),
			Type: "audio/mpeg",
		},
		PubDate: time.Unix(d.CreatedTime, 0).UTC().Format(http.TimeFormat),
	}
	if res.duration > 0 {
		it.Duration = formatItunesDuration(res.duration)
	}
	if imgURL := radiofrance.ImageURL(d.Visuals, d.MainImage); imgURL != "" {
		it.Image = &itunesImage{Href: imgURL}
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
