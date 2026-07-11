package feed

import "encoding/xml"

// These types model just enough of the RSS 2.0 + iTunes/Atom podcast feed
// format to serialize what this app produces; they aren't a general-purpose
// RSS library. Namespace prefixes are declared once on rss via raw
// "xmlns:prefix" attribute tags, and reused as plain "prefix:local" element
// tags elsewhere - encoding/xml treats these as opaque names and emits them
// verbatim, which is sufficient since we only ever marshal, never unmarshal,
// this structure.
type rss struct {
	XMLName xml.Name `xml:"rss"`
	Version string   `xml:"version,attr"`

	XMLNSItunes     string `xml:"xmlns:itunes,attr"`
	XMLNSPa         string `xml:"xmlns:pa,attr"`
	XMLNSPodcastRF  string `xml:"xmlns:podcastRF,attr"`
	XMLNSGoogleplay string `xml:"xmlns:googleplay,attr"`
	XMLNSAtom       string `xml:"xmlns:atom,attr"`

	Channel channel `xml:"channel"`
}

type channel struct {
	Title       string          `xml:"title"`
	Link        string          `xml:"link"`
	NextLink    *atomLink       `xml:"atom:link,omitempty"`
	Description string          `xml:"description"`
	Image       *itunesImage    `xml:"itunes:image,omitempty"`
	Category    *itunesCategory `xml:"itunes:category,omitempty"`
	Items       []item          `xml:"item"`
}

type item struct {
	Title       string       `xml:"title"`
	GUID        string       `xml:"guid"`
	Link        string       `xml:"link,omitempty"`
	Description string       `xml:"description"`
	Enclosure   enclosure    `xml:"enclosure"`
	PubDate     string       `xml:"pubDate"`
	Image       *itunesImage `xml:"itunes:image,omitempty"`
	// Duration is HH:MM:SS, omitted entirely when unknown (a cache miss
	// that failed, or caching disabled) rather than emitted as "00:00:00".
	Duration string `xml:"itunes:duration,omitempty"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
}

type itunesImage struct {
	Href string `xml:"href,attr"`
}

// itunesCategory renders as <itunes:category text="..."> with an optional
// nested subcategory of the same shape - the standard iTunes convention
// for category/subcategory.
type itunesCategory struct {
	Text        string          `xml:"text,attr"`
	Subcategory *itunesCategory `xml:"itunes:category,omitempty"`
}

type enclosure struct {
	URL  string `xml:"url,attr"`
	Type string `xml:"type,attr"`
}
