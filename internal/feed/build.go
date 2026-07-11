// Package feed builds the RSS/podcast XML feed served at /rss/{showId}.
package feed

import (
	"encoding/xml"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/Aerion/rss-radio-france-pour-tous/internal/radiofrance"
)

// guidCutoff is the boundary used to keep RSS guids stable for existing
// subscribers: diffusions created on or before this date keep using their
// manifestation ID as the guid (the original, pre-2022 scheme), while newer
// ones use the diffusion ID. Changing this would reset read/download state
// for every existing subscriber, so it must never move.
var guidCutoff = time.Date(2022, 9, 12, 0, 0, 0, 0, time.UTC)

// Builder builds RSS feeds for shows.
type Builder struct {
	// PublicBaseURL is this app's own externally-visible base URL, used to
	// build enclosure URLs that point back at our /audio/ redirect route.
	PublicBaseURL string
}

// Build renders an RSS 2.0 feed for a show's diffusions. nextPageURL, if
// non-empty, is advertised as an atom:link rel="next" for feed readers that
// support paginated feeds (RFC 5005).
func (b Builder) Build(diffusions []radiofrance.Diffusion, show radiofrance.Show, nextPageURL string) (string, error) {
	items := make([]item, 0, len(diffusions))
	for _, d := range diffusions {
		it, ok := b.buildItem(d)
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

func (b Builder) buildItem(d radiofrance.Diffusion) (item, bool) {
	manifestationID := d.ManifestationID()
	if manifestationID == "" {
		slog.Info("diffusion has no audio manifestation, skipping", "diffusionID", d.ID, "path", d.Path)
		return item{}, false
	}

	description := d.Standfirst
	if description == "" {
		description = d.BodyMarkdown
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
	if imgURL := radiofrance.ImageURL(d.Visuals, d.MainImage); imgURL != "" {
		it.Image = &itunesImage{Href: imgURL}
	}
	return it, true
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
